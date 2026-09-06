package fillerreview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func temporalStructureWindowWordlessEvidence(loaded temporalStructureWindowSuiteLoaded) ([]TemporalStructureWindowWordlessEvidence, error) {
	anchorBySource := make(map[string]TemporalStructureHoldoutAnchor, len(loaded.receipt.SelectedAnchors))
	for _, anchor := range loaded.receipt.SelectedAnchors {
		anchorBySource[anchor.SourceID] = anchor
	}
	evidenceByAlias := make(map[string]TemporalTruthEvidenceCase, len(loaded.evidenceManifest.Cases))
	for _, item := range loaded.evidenceManifest.Cases {
		evidenceByAlias[item.Alias] = item
	}
	privateByAlias := make(map[string]TemporalTruthEvidencePrivateEntry, len(loaded.evidencePrivateMap.Entries))
	for _, item := range loaded.evidencePrivateMap.Entries {
		privateByAlias[item.Alias] = item
	}
	var result []TemporalStructureWindowWordlessEvidence
	for _, item := range loaded.corpusAuthority.Cases {
		left, right, ok := temporalStructureWindowTargetJoin(item)
		if !ok {
			continue
		}
		measured := TemporalStructureWindowWordlessEvidence{
			CaseID: item.CaseID, Alias: item.Alias, TargetBoundaryMS: item.ObservedTargetBoundaryMS,
		}
		for _, sourceID := range []string{left.SourceID, right.SourceID} {
			anchor, anchorFound := anchorBySource[sourceID]
			evidence, evidenceFound := evidenceByAlias[anchor.EvidenceAlias]
			mapping, mappingFound := privateByAlias[anchor.EvidenceAlias]
			if !anchorFound || !evidenceFound || !mappingFound || mapping.CaseID != anchor.CaseID {
				return nil, fmt.Errorf("window wordless evidence cannot join source %q to transcript authority", sourceID)
			}
			if temporalStructureWindowTranscriptIsWordless(evidence.TranscriptSegments) {
				if !reviewSHA256(mapping.TranscriptSHA256) {
					return nil, fmt.Errorf("window wordless source %q lacks transcript identity", sourceID)
				}
				measured.Sources = append(measured.Sources, TemporalStructureWindowWordlessSourceProof{
					SourceID: sourceID, EvidenceAlias: anchor.EvidenceAlias, TranscriptSHA256: mapping.TranscriptSHA256,
				})
			}
		}
		if len(measured.Sources) > 0 {
			result = append(result, measured)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CaseID < result[j].CaseID })
	if len(result) < fillerstructurewindowcert.MinimumSliceCases {
		return nil, fmt.Errorf("window corpus has %d independently measured wordless joins; need %d", len(result), fillerstructurewindowcert.MinimumSliceCases)
	}
	return result, nil
}

func temporalStructureWindowTargetJoin(item TemporalStructureWindowMediaAuthorityCase) (TemporalStructureChallengeAuthorityPart, TemporalStructureChallengeAuthorityPart, bool) {
	if item.ObservedTargetBoundaryMS <= 0 {
		return TemporalStructureChallengeAuthorityPart{}, TemporalStructureChallengeAuthorityPart{}, false
	}
	for index := 0; index+1 < len(item.Parts); index++ {
		if item.Parts[index].OutputEndMS == item.ObservedTargetBoundaryMS &&
			item.Parts[index+1].OutputStartMS == item.ObservedTargetBoundaryMS {
			return item.Parts[index], item.Parts[index+1], true
		}
	}
	return TemporalStructureChallengeAuthorityPart{}, TemporalStructureChallengeAuthorityPart{}, false
}

func temporalStructureWindowTranscriptIsWordless(segments []fillerbakeoff.TranscriptSegment) bool {
	if len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		switch strings.ToLower(strings.TrimSpace(segment.Text)) {
		case "[applause]", "[laughter]", "[music]", "[silence]":
		default:
			return false
		}
	}
	return true
}

func temporalStructureWindowMotionEvidence(ctx context.Context, config TemporalStructureWindowSuiteConfig, loaded temporalStructureWindowSuiteLoaded) ([]TemporalStructureWindowMotionEvidence, int64, error) {
	privateByAlias := make(map[string]TemporalStructureWindowSetAuthorityCase, len(loaded.windowSetAuthority.Cases))
	for _, item := range loaded.windowSetAuthority.Cases {
		privateByAlias[item.Alias] = item
	}
	root := filepath.Dir(config.WindowSetManifestPath)
	var result []TemporalStructureWindowMotionEvidence
	best := make(map[string]int)
	for _, item := range loaded.windowSetManifest.Cases {
		private, ok := privateByAlias[item.Alias]
		if !ok {
			return nil, 0, errors.New("window motion measurement lacks private case identity")
		}
		for ordinal, window := range item.Windows {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			media := item.MediaSet.Windows[ordinal].Media
			sample, err := config.Motion.Measure(ctx, filepath.Join(root, filepath.FromSlash(window.Path)))
			if err != nil {
				return nil, 0, fmt.Errorf("measure motion case %q window %d: %w", item.Alias, ordinal, err)
			}
			if !validTemporalStructureWindowMotionSample(sample) || sample.Frames < media.DurationMS*20/1_000 || sample.Frames > media.DurationMS*40/1_000+1 {
				return nil, 0, fmt.Errorf("measure motion case %q window %d returned incomplete sample", item.Alias, ordinal)
			}
			measured := TemporalStructureWindowMotionEvidence{
				CaseID: private.CaseID, Alias: item.Alias, WindowOrdinal: ordinal,
				MediaSHA256: media.SHA256, MediaDurationMS: media.DurationMS,
				Frames: sample.Frames, SumMicroluma: sample.SumMicroluma,
				MeanMicroluma: roundedMotionMean(sample.SumMicroluma, sample.Frames),
				P95Microluma:  sample.P95Microluma, MaximumMicroluma: sample.MaximumMicroluma,
			}
			result = append(result, measured)
			index := len(result) - 1
			if current, found := best[private.CaseID]; !found || motionEvidenceGreater(result[index], result[current]) {
				best[private.CaseID] = index
			}
		}
	}
	if len(best) < fillerstructurewindowcert.MinimumSliceCases {
		return nil, 0, errors.New("window motion evidence lacks six distinct cases")
	}
	ranked := make([]int, 0, len(best))
	for _, index := range best {
		ranked = append(ranked, index)
	}
	sort.Slice(ranked, func(i, j int) bool {
		left, right := result[ranked[i]], result[ranked[j]]
		if motionEvidenceGreater(left, right) {
			return true
		}
		if motionEvidenceGreater(right, left) {
			return false
		}
		if left.CaseID != right.CaseID {
			return left.CaseID < right.CaseID
		}
		return left.WindowOrdinal < right.WindowOrdinal
	})
	for _, index := range ranked[:fillerstructurewindowcert.MinimumSliceCases] {
		result[index].Selected = true
	}
	minimum := result[ranked[fillerstructurewindowcert.MinimumSliceCases-1]].MeanMicroluma
	if minimum <= 0 {
		return nil, 0, errors.New("window motion evidence did not measure a non-zero high-motion cohort")
	}
	return result, minimum, nil
}

func motionEvidenceGreater(left, right TemporalStructureWindowMotionEvidence) bool {
	return left.SumMicroluma*right.Frames > right.SumMicroluma*left.Frames
}

func roundedMotionMean(sum, frames int64) int64 {
	return (sum + frames/2) / frames
}
