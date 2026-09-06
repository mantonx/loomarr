package fillerreview

import (
	"fmt"
	"slices"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func validateTemporalStructureHoldoutMultiCompilations(items []TemporalStructureHoldoutMultiCompilation, cases map[string]TemporalStructureChallengeCase, anchors map[string]TemporalStructureHoldoutAnchor) error {
	traits := map[string]int{}
	usage := map[string]int{}
	seenCases := map[string]struct{}{}
	for _, item := range items {
		challenge, exists := cases[item.CaseID]
		if !exists || challenge.Unit != fillereval.UnitCompilation || !slices.Equal(challenge.Slices, []string{item.Trait, TemporalStructureSliceThreeItemCompilation}) || len(item.SourceIDs) != 3 || len(item.Roles) != 3 || len(item.JoinTimesMS) != 2 || len(challenge.Segments) != 3 {
			return fmt.Errorf("temporal structure holdout contains an invalid multi-item compilation")
		}
		if _, duplicate := seenCases[item.CaseID]; duplicate {
			return fmt.Errorf("temporal structure holdout repeats a multi-item compilation case")
		}
		seenCases[item.CaseID] = struct{}{}
		seenSources := map[string]struct{}{}
		var durationMS int64
		for index, sourceID := range item.SourceIDs {
			anchor, anchorExists := anchors[sourceID]
			if !anchorExists || item.Roles[index] != anchor.Role || challenge.Segments[index] != (TemporalStructureChallengeSegment{SourceID: sourceID, DurationMS: anchor.DurationMS}) {
				return fmt.Errorf("temporal structure holdout multi-item compilation drifts from an anchor")
			}
			if _, duplicate := seenSources[sourceID]; duplicate {
				return fmt.Errorf("temporal structure holdout multi-item compilation repeats a source")
			}
			seenSources[sourceID] = struct{}{}
			if index > 0 && item.JoinTimesMS[index-1] != durationMS {
				return fmt.Errorf("temporal structure holdout multi-item compilation join drift")
			}
			durationMS += anchor.DurationMS
			usage[sourceID]++
		}
		if item.DurationMS != durationMS || item.Trait != temporalStructureMultiTrait(item.Roles) {
			return fmt.Errorf("temporal structure holdout multi-item compilation duration or trait drift")
		}
		traits[item.Trait]++
	}
	if traits[temporalStructureMultiSameRoleJoin] != 6 || traits[temporalStructureMultiMixedRoleJoins] != 6 || len(traits) != 2 || len(usage) != len(anchors) {
		return fmt.Errorf("temporal structure holdout multi-item join coverage is incomplete")
	}
	for _, count := range usage {
		if count != 3 {
			return fmt.Errorf("temporal structure holdout multi-item source coverage is unbalanced")
		}
	}
	return nil
}
