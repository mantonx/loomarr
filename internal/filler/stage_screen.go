package filler

import (
	"context"
	"path/filepath"
	"strings"
)

// SegmentScreeningStage is the rendered-child safety boundary between structure materialization
// and metadata enrichment. It deliberately uses the concrete coordinator and certification
// modules: those deep modules own retry settlement and release replay, while this adapter only
// resolves portable media paths and translates their closed outcomes into pipeline verdicts.
type SegmentScreeningStage struct {
	runtime       *SegmentScreeningRuntime
	certification *SegmentScreeningCertification
	clipDir       string
}

func NewSegmentScreeningStage(runtime *SegmentScreeningRuntime, certification *SegmentScreeningCertification, clipDir string) *SegmentScreeningStage {
	return &SegmentScreeningStage{runtime: runtime, certification: certification, clipDir: clipDir}
}

func (s *SegmentScreeningStage) ID() StageID     { return StageScreen }
func (s *SegmentScreeningStage) Cost() StageCost { return CostVision }

func (s *SegmentScreeningStage) Applies(_ context.Context, clip StoreClip) (bool, string) {
	if !clip.IsSegment() {
		return false, "not a materialized compilation child"
	}
	return true, ""
}

func (s *SegmentScreeningStage) Run(ctx context.Context, clip StoreClip) (StageResult, error) {
	if err := ctx.Err(); err != nil {
		return StageResult{}, err
	}
	if s == nil || s.runtime == nil {
		return segmentScreeningReview(clip, "complete rendered-child screening is not configured"), nil
	}
	if !clip.IsSegment() || !isContentHash(clip.Hash) || clip.Path == "" ||
		s.clipDir == "" || !filepath.IsAbs(s.clipDir) || filepath.Clean(s.clipDir) != s.clipDir {
		return segmentScreeningReview(clip, "rendered-child identity is incomplete"), nil
	}

	playbackPath := filepath.Join(s.clipDir, filepath.FromSlash(clip.Path))
	tags, state := ReadSidecarTagsState(playbackPath)
	if state != SidecarValid {
		return segmentScreeningReview(clip, "rendered-child sidecar is missing or invalid"), nil
	}
	subject, err := NewSegmentScreeningSubject(clip.Hash, tags)
	if err != nil || subject.Lineage == nil || subject.Lineage.ParentHash != clip.ParentHash {
		return segmentScreeningReview(clip, "rendered-child media lineage is incomplete or drifted"), nil
	}
	media := SegmentScreeningMedia{
		Subject:          subject,
		Manifest:         *tags.MediaAssets,
		SourceMasterPath: filepath.Join(s.clipDir, filepath.FromSlash(tags.MediaAssets.SourceMaster.Path)),
		EvidencePath:     filepath.Join(s.clipDir, filepath.FromSlash(tags.MediaAssets.Evidence.Asset.Path)),
		PlaybackPath:     playbackPath,
	}
	aggregate, err := s.runtime.Screen(ctx, media)
	if err != nil {
		if ctx.Err() != nil {
			return StageResult{}, ctx.Err()
		}
		return segmentScreeningReview(clip, "a rendered-child screening authority is unavailable"), nil
	}
	reference, err := NewSegmentScreeningReference(subject, aggregate)
	if err != nil {
		return segmentScreeningReview(clip, "rendered-child screening evidence is invalid"), nil
	}
	tags.SegmentScreening = &reference
	if err := WriteSidecarTags(playbackPath, tags, false); err != nil {
		return segmentScreeningReview(clip, "rendered-child screening evidence could not be attached"), nil
	}
	reportProgress(ctx, StageScreen, 100)

	if axes := segmentScreeningAxesWithOutcome(aggregate, ScreenReject); len(axes) != 0 {
		return StageResult{
			Clip: clip, Verdict: VerdictReject, Reason: ReasonScreening,
			Detail: "rendered child rejected by " + strings.Join(axes, ", "),
		}, nil
	}
	if axes := segmentScreeningAxesWithOutcome(aggregate, ScreenHold); len(axes) != 0 {
		return segmentScreeningReview(clip, "rendered-child screening requires review: "+strings.Join(axes, ", ")), nil
	}
	if !aggregate.Passes() {
		return segmentScreeningReview(clip, "rendered-child screening did not produce five passes"), nil
	}
	if s.certification == nil {
		return segmentScreeningReview(clip, "rendered-child screening passed but production release is not authorized"), nil
	}
	if err := s.certification.Verify(ctx, aggregate); err != nil {
		if ctx.Err() != nil {
			return StageResult{}, ctx.Err()
		}
		return segmentScreeningReview(clip, "rendered-child screening passed but terminal release replay failed"), nil
	}
	return StageResult{Clip: clip, Verdict: VerdictContinue}, nil
}

func segmentScreeningReview(clip StoreClip, note string) StageResult {
	return StageResult{Clip: clip, Verdict: VerdictReview, Note: note}
}

func segmentScreeningAxesWithOutcome(evidence SegmentScreeningEvidence, outcome SegmentScreeningOutcome) []string {
	axes := make([]string, 0, len(evidence.Results))
	for _, result := range evidence.Results {
		if result.Outcome == outcome {
			axes = append(axes, string(result.Axis))
		}
	}
	return axes
}

var _ Stage = (*SegmentScreeningStage)(nil)
