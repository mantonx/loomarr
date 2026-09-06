package fillerreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func buildTemporalStructureWindowMediaCase(ctx context.Context, media TemporalStructureWindowCorpusMedia, publicRoot string, item temporalStructureWindowPreparedCase) (TemporalStructureWindowMediaPublicCase, TemporalStructureWindowMediaAuthorityCase, error) {
	caseRoot := filepath.Join(publicRoot, "cases", item.media.alias)
	if err := os.MkdirAll(caseRoot, 0o750); err != nil {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, err
	}
	outputPath := filepath.Join(caseRoot, "source.mp4")
	rendered, err := media.Render(ctx, item.media.segments, outputPath)
	if err != nil {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, err
	}
	if len(rendered.Parts) != len(item.media.segments) || len(rendered.Parts) != len(item.planCase.Truth) {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, errors.New("media adapter returned incomplete window corpus part authority")
	}
	profile := fillerstructuremedia.CanonicalProfile()
	if rendered.Video.DurationMS <= fillerstructurewindow.PrimarySpanMS ||
		rendered.Video.DurationMS > fillerstructurewindow.MaximumSourceDurationMS ||
		rendered.Video.Width != profile.Width || rendered.Video.Height != profile.Height {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, errors.New("rendered window corpus source does not match the canonical profile or duration range")
	}
	if err := media.Decode(ctx, outputPath); err != nil {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, fmt.Errorf("complete audio/video decode: %w", err)
	}
	info, err := os.Lstat(outputPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > TemporalStructureWindowMaximumSourceBytes {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, errors.New("rendered window corpus source is not a bounded regular file")
	}
	digest, size, err := filler.FileSHA256(outputPath)
	if err != nil {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, err
	}
	clipHash, err := filler.ClipID(outputPath)
	if err != nil {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, err
	}
	relativePath := filepath.ToSlash(filepath.Join("cases", item.media.alias, "source.mp4"))
	source := filler.SplitSourceAsset{
		Role: filler.SplitSourceLegacyPlayback, SHA256: digest, Bytes: size, ClipHash: clipHash,
		Path: relativePath, DurationMs: rendered.Video.DurationMS,
	}
	plan, err := fillerstructurewindow.NewPlan(fillerstructure.Source{SHA256: digest, Bytes: size, DurationMS: rendered.Video.DurationMS})
	if err != nil {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, err
	}
	publicCase := TemporalStructureWindowMediaPublicCase{
		Alias: item.media.alias, Source: source, Plan: plan,
		Video: TemporalStructureWindowVideo{Width: rendered.Video.Width, Height: rendered.Video.Height},
	}
	authorityCase, err := temporalStructureWindowMediaAuthorityCase(item, publicCase, rendered)
	if err != nil {
		return TemporalStructureWindowMediaPublicCase{}, TemporalStructureWindowMediaAuthorityCase{}, err
	}
	return publicCase, authorityCase, nil
}

func temporalStructureWindowMediaAuthorityCase(item temporalStructureWindowPreparedCase, publicCase TemporalStructureWindowMediaPublicCase, rendered TemporalStructureRenderResult) (TemporalStructureWindowMediaAuthorityCase, error) {
	authority := TemporalStructureWindowMediaAuthorityCase{Alias: publicCase.Alias, CaseID: item.planCase.ID}
	var outputStart int64
	for index, part := range rendered.Parts {
		if part.DurationMS <= 0 || absoluteInt64(part.DurationMS-item.media.segments[index].DurationMS) > fillerstructure.AssessmentMediaMaximumTimelineDriftMS {
			return TemporalStructureWindowMediaAuthorityCase{}, fmt.Errorf("rendered window corpus part %d duration drifted: requested=%dms rendered=%dms", index, item.media.segments[index].DurationMS, part.DurationMS)
		}
		outputEnd := outputStart + part.DurationMS
		source := item.media.sources[index]
		authority.Parts = append(authority.Parts, TemporalStructureChallengeAuthorityPart{
			Ordinal: index, SourceID: source.ID, SourcePath: source.Path, SourceSHA256: source.SHA256,
			SourceDurationMS: source.DurationMS, SourceRole: source.StandaloneRole,
			SourceStartMS: item.media.segments[index].StartMS, RequestedMS: item.media.segments[index].DurationMS,
			RenderedMS: part.DurationMS, OutputStartMS: outputStart, OutputEndMS: outputEnd, Provenance: source.Provenance,
		})
		authority.Truth = append(authority.Truth, fillerstructure.Segment{
			StartMS: outputStart, EndMS: outputEnd, Role: item.planCase.Truth[index].Role,
		})
		outputStart = outputEnd
	}
	if absoluteInt64(outputStart-publicCase.Source.DurationMs) > fillerstructure.AssessmentMediaMaximumTimelineDriftMS {
		return TemporalStructureWindowMediaAuthorityCase{}, errors.New("rendered window corpus parts do not bind output duration")
	}
	last := len(authority.Truth) - 1
	if publicCase.Source.DurationMs <= authority.Truth[last].StartMS {
		return TemporalStructureWindowMediaAuthorityCase{}, errors.New("window corpus final duration reconciliation erased its last part")
	}
	authority.Truth[last].EndMS = publicCase.Source.DurationMs
	if item.planCase.Pattern == TemporalStructureWindowPatternSeamOverlap ||
		item.planCase.Pattern == TemporalStructureWindowPatternSeamPrimaryLeft ||
		item.planCase.Pattern == TemporalStructureWindowPatternSeamPrimaryRight {
		authority.ObservedTargetBoundaryMS = authority.Truth[1].EndMS
	}
	if err := validateTemporalStructureWindowMediaCase(publicCase, authority, item.planCase); err != nil {
		return TemporalStructureWindowMediaAuthorityCase{}, err
	}
	return authority, nil
}
