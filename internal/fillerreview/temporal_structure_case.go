package fillerreview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func buildTemporalStructureChallengeCase(ctx context.Context, config TemporalStructureChallengeConfig, publicRoot string, item temporalStructurePreparedCase) (TemporalStructureChallengePublicCase, TemporalStructureChallengeAuthorityCase, error) {
	caseRoot := filepath.Join(publicRoot, "cases", item.alias)
	if err := os.MkdirAll(caseRoot, 0o750); err != nil {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, err
	}
	videoPath := filepath.Join(caseRoot, "video.mp4")
	rendered, err := config.Media.Render(ctx, item.segments, videoPath)
	if err != nil {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, err
	}
	if len(rendered.Parts) != len(item.segments) || rendered.Video.DurationMS <= 0 || rendered.Video.Width <= 0 || rendered.Video.Height <= 0 {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, fmt.Errorf("media adapter returned incomplete render authority")
	}
	if err := validateTemporalStructureVideoProfile(rendered.Video); err != nil {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, err
	}
	digest, err := hashFile(videoPath)
	if err != nil {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, err
	}
	info, err := os.Stat(videoPath)
	if err != nil {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, fmt.Errorf("inspect rendered video size: %w", err)
	}
	if info.Size() <= 0 || info.Size() > TemporalTruthMaximumVideoBytes {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, fmt.Errorf("rendered video size %d exceeds allowed range 1..%d bytes", info.Size(), TemporalTruthMaximumVideoBytes)
	}
	publicCase := TemporalStructureChallengePublicCase{Alias: item.alias, Video: TemporalTruthEvidenceFile{
		Path: filepath.ToSlash(filepath.Join("cases", item.alias, "video.mp4")), SHA256: digest, Bytes: info.Size(),
		DurationMS: rendered.Video.DurationMS, Width: rendered.Video.Width, Height: rendered.Video.Height,
	}, Profile: rendered.Video.Profile}
	authorityCase := TemporalStructureChallengeAuthorityCase{Alias: item.alias, CaseID: item.spec.ID, Unit: item.spec.Unit, Role: item.spec.Role, Slices: append([]string(nil), item.spec.Slices...), VideoSHA256: digest}
	outputStart := int64(0)
	for index, part := range rendered.Parts {
		if part.DurationMS <= 0 || absoluteInt64(part.DurationMS-item.segments[index].DurationMS) > 1_000 {
			return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, fmt.Errorf("rendered segment %d duration drift", index)
		}
		outputEnd := outputStart + part.DurationMS
		source := item.sources[index]
		authorityCase.Segments = append(authorityCase.Segments, TemporalStructureChallengeAuthorityPart{
			Ordinal: index, SourceID: source.ID, SourcePath: source.Path, SourceSHA256: source.SHA256,
			SourceDurationMS: source.DurationMS, SourceRole: source.StandaloneRole, SourceStartMS: item.segments[index].StartMS,
			RequestedMS: item.segments[index].DurationMS, RenderedMS: part.DurationMS,
			OutputStartMS: outputStart, OutputEndMS: outputEnd, Provenance: source.Provenance,
		})
		if index > 0 {
			authorityCase.JoinTimesMS = append(authorityCase.JoinTimesMS, outputStart)
		}
		outputStart = outputEnd
	}
	if absoluteInt64(outputStart-rendered.Video.DurationMS) > 1_000 {
		return TemporalStructureChallengePublicCase{}, TemporalStructureChallengeAuthorityCase{}, fmt.Errorf("rendered parts do not bind output duration: parts=%dms output=%dms", outputStart, rendered.Video.DurationMS)
	}
	return publicCase, authorityCase, nil
}
