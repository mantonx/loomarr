package filler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/mediatools"
)

type preparedStructureAssessmentSource struct {
	SnapshotPath string
	Identity     StructureAssessmentMediaSourceIdentity
	Profile      fillerstructuremedia.Profile
	Tool         mediatools.MediaToolIdentity
}

// prepareSource snapshots and verifies the complete source once before any assessment derivative
// is rendered. Callers own removal of SnapshotPath.
func (p *FFmpegStructureAssessmentMediaPreparer) prepareSource(ctx context.Context, input StructureAssessmentSource) (preparedStructureAssessmentSource, error) {
	if p == nil || p.probe == nil || p.identify == nil || p.snapshot == nil {
		return preparedStructureAssessmentSource{}, errors.New("structure assessment media preparer is unavailable")
	}
	if err := input.Source.validate(); err != nil {
		return preparedStructureAssessmentSource{}, fmt.Errorf("structure assessment source: %w", err)
	}
	expectedSource := filepath.Join(p.sourceRoot, filepath.FromSlash(input.Source.Path))
	if !filepath.IsAbs(input.FullPath) || filepath.Clean(input.FullPath) != input.FullPath || input.FullPath != expectedSource {
		return preparedStructureAssessmentSource{}, errors.New("structure assessment source path does not match its retained identity")
	}
	info, err := os.Lstat(input.FullPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != input.Source.Bytes {
		return preparedStructureAssessmentSource{}, errors.New("structure assessment source is not the declared regular file")
	}

	stageDir := filepath.Join(p.mediaRoot, MediaAssetRootName, ".staging")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return preparedStructureAssessmentSource{}, fmt.Errorf("create structure assessment staging: %w", err)
	}
	snapshotMarker, err := os.CreateTemp(stageDir, "structure-source-*")
	if err != nil {
		return preparedStructureAssessmentSource{}, err
	}
	snapshotPath := snapshotMarker.Name()
	if err := snapshotMarker.Close(); err != nil {
		_ = os.Remove(snapshotPath)
		return preparedStructureAssessmentSource{}, err
	}
	_ = os.Remove(snapshotPath)
	if err := p.snapshot(ctx, input.FullPath, snapshotPath); err != nil {
		_ = os.Remove(snapshotPath)
		return preparedStructureAssessmentSource{}, fmt.Errorf("snapshot structure assessment source: %w", err)
	}
	fail := func(err error) (preparedStructureAssessmentSource, error) {
		_ = os.Remove(snapshotPath)
		return preparedStructureAssessmentSource{}, err
	}
	if err := validateStructureAssessmentSourceSnapshot(snapshotPath, input.Source); err != nil {
		return fail(err)
	}
	inputProbe, err := p.probe(ctx, snapshotPath)
	if err != nil || inputProbe.NoVideo || inputProbe.Silent || inputProbe.Width <= 0 || inputProbe.Height <= 0 ||
		absoluteDurationDifference(inputProbe.DurationMs, input.Source.DurationMs) > fillerstructure.AssessmentMediaMaximumTimelineDriftMS {
		return fail(errors.New("structure assessment source streams or duration do not match authority"))
	}
	tool, err := p.identify(ctx, p.ffmpegPath)
	if err != nil {
		return fail(fmt.Errorf("identify structure assessment media tool: %w", err))
	}
	return preparedStructureAssessmentSource{
		SnapshotPath: snapshotPath, Identity: structureAssessmentSourceIdentity(input.Source),
		Profile: fillerstructuremedia.CanonicalProfile(), Tool: tool,
	}, nil
}
