package filler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/mediatools"
)

// PrepareWindows renders and verifies every normalized interval in one immutable plan. It returns
// no usable authority unless the complete ordered media set validates.
func (p *FFmpegStructureAssessmentMediaPreparer) PrepareWindows(ctx context.Context, input StructureAssessmentSource, plan fillerstructurewindow.Plan) (StructureAssessmentWindowMediaSet, error) {
	if p == nil || p.run == nil || p.decode == nil {
		return StructureAssessmentWindowMediaSet{}, errors.New("structure assessment window preparer is unavailable")
	}
	if err := fillerstructurewindow.ValidatePlan(plan); err != nil {
		return StructureAssessmentWindowMediaSet{}, err
	}
	wantSource := fillerstructure.Source{SHA256: input.Source.SHA256, Bytes: input.Source.Bytes, DurationMS: input.Source.DurationMs}
	if plan.Source != wantSource {
		return StructureAssessmentWindowMediaSet{}, errors.New("structure assessment window plan does not bind the supplied source")
	}
	prepared, err := p.prepareSource(ctx, input)
	if err != nil {
		return StructureAssessmentWindowMediaSet{}, err
	}
	defer func() { _ = os.Remove(prepared.SnapshotPath) }()

	result := StructureAssessmentWindowMediaSet{Source: input.Source}
	identities := make([]fillerstructure.AssessmentMedia, 0, len(plan.Windows))
	for _, window := range plan.Windows {
		if err := ctx.Err(); err != nil {
			return StructureAssessmentWindowMediaSet{}, err
		}
		media, err := p.prepareWindow(ctx, plan, prepared, window)
		if err != nil {
			return StructureAssessmentWindowMediaSet{}, fmt.Errorf("prepare structure assessment window %d: %w", window.Ordinal, err)
		}
		result.Windows = append(result.Windows, media)
		identities = append(identities, media.Media.Media)
	}
	authority, err := fillerstructurewindow.NewMediaSet(plan, identities)
	if err != nil {
		return StructureAssessmentWindowMediaSet{}, err
	}
	result.Authority = authority
	if err := validatePreparedStructureAssessmentWindows(result); err != nil {
		return StructureAssessmentWindowMediaSet{}, err
	}
	return result, nil
}

func (p *FFmpegStructureAssessmentMediaPreparer) prepareWindow(ctx context.Context, plan fillerstructurewindow.Plan, prepared preparedStructureAssessmentSource, window fillerstructurewindow.Window) (StructureAssessmentWindowMedia, error) {
	operation := structureAssessmentWindowOperationSHA256(plan, prepared.Identity, window, prepared.Profile, prepared.Tool)
	if !isContentHash(operation) {
		return StructureAssessmentWindowMedia{}, errors.New("structure assessment window operation identity is invalid")
	}
	indexPath := structureAssessmentWindowIndexPath(p.mediaRoot, operation)
	index, found, err := loadStructureAssessmentWindowIndex(indexPath, operation)
	if err != nil {
		return StructureAssessmentWindowMedia{}, err
	}
	if found {
		return p.reuseWindow(ctx, plan, prepared, window, operation, index)
	}

	stageDir := filepath.Join(p.mediaRoot, MediaAssetRootName, ".staging")
	output, err := os.CreateTemp(stageDir, "structure-window-*.mp4")
	if err != nil {
		return StructureAssessmentWindowMedia{}, err
	}
	outputPath := output.Name()
	if err := output.Close(); err != nil {
		_ = os.Remove(outputPath)
		return StructureAssessmentWindowMedia{}, err
	}
	_ = os.Remove(outputPath)
	defer func() { _ = os.Remove(outputPath) }()
	durationMS := window.MediaEndMS - window.MediaStartMS
	arguments := fillerstructuremedia.PartArguments(prepared.SnapshotPath, window.MediaStartMS, durationMS, outputPath)
	if err := p.run(ctx, mediatools.FFmpegOr(p.ffmpegPath), arguments); err != nil {
		return StructureAssessmentWindowMedia{}, fmt.Errorf("render: %w", err)
	}
	probe, err := p.probe(ctx, outputPath)
	if err != nil {
		return StructureAssessmentWindowMedia{}, fmt.Errorf("probe: %w", err)
	}
	if err := validateStructureAssessmentWindowOutput(outputPath, probe, window, plan.Profile); err != nil {
		return StructureAssessmentWindowMedia{}, err
	}
	if err := p.decode(ctx, p.ffmpegPath, outputPath); err != nil {
		return StructureAssessmentWindowMedia{}, fmt.Errorf("decode: %w", err)
	}
	mediaSHA, mediaBytes, err := FileSHA256(outputPath)
	if err != nil {
		return StructureAssessmentWindowMedia{}, err
	}
	lineage := StructureAssessmentWindowLineage{
		SchemaVersion:   structureAssessmentWindowLineageSchemaVersion,
		ContractVersion: structureAssessmentWindowLineageContractVersion,
		OperationSHA256: operation, PlanSHA256: plan.SHA256, Source: prepared.Identity,
		Window: window, Profile: prepared.Profile, Tool: prepared.Tool,
		Media: StructureAssessmentMediaDerivative{SHA256: mediaSHA, Bytes: mediaBytes, DurationMS: probe.DurationMs},
	}
	lineage.SHA256 = structureAssessmentWindowLineageSHA256(lineage)
	if err := validateStructureAssessmentWindowLineage(plan, lineage); err != nil {
		return StructureAssessmentWindowMedia{}, err
	}
	mediaPath := structureAssessmentMediaPath(p.mediaRoot, mediaSHA)
	if err := publishStructureAssessmentMedia(ctx, outputPath, mediaPath, mediaSHA, mediaBytes); err != nil {
		return StructureAssessmentWindowMedia{}, fmt.Errorf("publish media: %w", err)
	}
	if err := p.publishWindowArtifacts(ctx, stageDir, indexPath, operation, lineage); err != nil {
		return StructureAssessmentWindowMedia{}, err
	}
	return preparedStructureAssessmentWindowMedia(window, lineage, mediaPath), nil
}

func (p *FFmpegStructureAssessmentMediaPreparer) publishWindowArtifacts(ctx context.Context, stageDir, indexPath, operation string, lineage StructureAssessmentWindowLineage) error {
	lineageStage, lineageRaw, err := writeStructureAssessmentJSON(stageDir, "structure-window-lineage-*", lineage)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(lineageStage) }()
	if err := publishStructureAssessmentArtifact(ctx, lineageStage, structureAssessmentWindowLineagePath(p.mediaRoot, lineage.SHA256), lineageRaw); err != nil {
		return fmt.Errorf("publish lineage: %w", err)
	}
	index := structureAssessmentWindowIndex{
		SchemaVersion:   structureAssessmentWindowIndexSchemaVersion,
		ContractVersion: structureAssessmentWindowIndexContractVersion,
		OperationSHA256: operation, LineageSHA256: lineage.SHA256,
	}
	indexStage, indexRaw, err := writeStructureAssessmentJSON(stageDir, "structure-window-index-*", index)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(indexStage) }()
	if err := publishStructureAssessmentArtifact(ctx, indexStage, indexPath, indexRaw); err != nil {
		return fmt.Errorf("publish index: %w", err)
	}
	return nil
}

func (p *FFmpegStructureAssessmentMediaPreparer) reuseWindow(ctx context.Context, plan fillerstructurewindow.Plan, prepared preparedStructureAssessmentSource, window fillerstructurewindow.Window, operation string, index structureAssessmentWindowIndex) (StructureAssessmentWindowMedia, error) {
	lineage, err := loadStructureAssessmentWindowLineage(structureAssessmentWindowLineagePath(p.mediaRoot, index.LineageSHA256), index.LineageSHA256, plan)
	if err != nil {
		return StructureAssessmentWindowMedia{}, err
	}
	if lineage.OperationSHA256 != operation || lineage.Source != prepared.Identity || lineage.Window != window ||
		lineage.Profile != prepared.Profile || lineage.Tool != prepared.Tool {
		return StructureAssessmentWindowMedia{}, errors.New("structure assessment window reuse authority drifted")
	}
	mediaPath := structureAssessmentMediaPath(p.mediaRoot, lineage.Media.SHA256)
	mediaSHA, mediaBytes, err := FileSHA256(mediaPath)
	if err != nil || mediaSHA != lineage.Media.SHA256 || mediaBytes != lineage.Media.Bytes {
		return StructureAssessmentWindowMedia{}, errors.New("reused structure assessment window bytes do not match lineage")
	}
	probe, err := p.probe(ctx, mediaPath)
	if err != nil {
		return StructureAssessmentWindowMedia{}, fmt.Errorf("probe reused window: %w", err)
	}
	if err := validateStructureAssessmentWindowOutput(mediaPath, probe, window, plan.Profile); err != nil ||
		probe.DurationMs != lineage.Media.DurationMS {
		return StructureAssessmentWindowMedia{}, errors.New("reused structure assessment window profile does not match lineage")
	}
	if err := p.decode(ctx, p.ffmpegPath, mediaPath); err != nil {
		return StructureAssessmentWindowMedia{}, fmt.Errorf("decode reused window: %w", err)
	}
	return preparedStructureAssessmentWindowMedia(window, lineage, mediaPath), nil
}

func validateStructureAssessmentWindowOutput(path string, probe Probed, window fillerstructurewindow.Window, profile fillerstructurewindow.Profile) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > profile.MaximumWindowBytes {
		return errors.New("structure assessment window is not a bounded regular file")
	}
	sampleAspect := strings.ReplaceAll(probe.SampleAspect, ":", "/")
	wantProfile := fillerstructuremedia.CanonicalProfile()
	wantDuration := window.MediaEndMS - window.MediaStartMS
	durationDrift := absoluteDurationDifference(probe.DurationMs, wantDuration)
	if probe.NoVideo || probe.Silent || probe.Width != wantProfile.Width || probe.Height != wantProfile.Height ||
		probe.Cadence != wantProfile.FrameRate || sampleAspect != wantProfile.SampleAspectRatio || probe.DurationMs <= 0 ||
		durationDrift > profile.MaximumTimelineDriftMS {
		return fmt.Errorf("structure assessment window does not match the canonical profile: video=%t audio=%t dimensions=%dx%d want=%dx%d cadence=%q want=%q sample_aspect=%q want=%q duration_ms=%d want=%d drift_ms=%d maximum_drift_ms=%d",
			!probe.NoVideo, !probe.Silent, probe.Width, probe.Height, wantProfile.Width, wantProfile.Height,
			probe.Cadence, wantProfile.FrameRate, sampleAspect, wantProfile.SampleAspectRatio,
			probe.DurationMs, wantDuration, durationDrift, profile.MaximumTimelineDriftMS)
	}
	return nil
}

func preparedStructureAssessmentWindowMedia(window fillerstructurewindow.Window, lineage StructureAssessmentWindowLineage, path string) StructureAssessmentWindowMedia {
	return StructureAssessmentWindowMedia{
		Window: window,
		Media: fillerstructurewindow.WindowMedia{Ordinal: window.Ordinal, Media: fillerstructure.AssessmentMedia{
			SHA256: lineage.Media.SHA256, Bytes: lineage.Media.Bytes, DurationMS: lineage.Media.DurationMS,
			ProfileSHA256: lineage.Profile.SHA256, LineageSHA256: lineage.SHA256,
		}},
		FullPath: path,
	}
}

func validatePreparedStructureAssessmentWindows(result StructureAssessmentWindowMediaSet) error {
	if err := fillerstructurewindow.ValidateMediaSet(result.Authority); err != nil {
		return err
	}
	if len(result.Windows) != len(result.Authority.Windows) {
		return errors.New("prepared structure assessment window set is incomplete")
	}
	for ordinal, media := range result.Windows {
		if media.Window != result.Authority.Plan.Windows[ordinal] || media.Media != result.Authority.Windows[ordinal] ||
			!filepath.IsAbs(media.FullPath) || filepath.Clean(media.FullPath) != media.FullPath {
			return errors.New("prepared structure assessment window set drifted from authority")
		}
	}
	return nil
}
