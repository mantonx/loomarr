package fillerreview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	temporalStructureMultiSameRoleJoin   = TemporalStructureSliceAdjacentSameRole
	temporalStructureMultiMixedRoleJoins = TemporalStructureSliceMixedRoleJoins
)

// The schedule is balanced over the holdout's fixed 2/3/2/2/3 role quotas.
// Each source occurs exactly three times. The first half contains a same-role
// adjacency; the second half contains only mixed-role adjacencies.
var temporalStructureMultiSchedule = [][3]int{
	{0, 1, 2}, {0, 1, 3}, {0, 1, 4}, {2, 3, 4}, {2, 3, 5}, {4, 5, 6},
	{5, 7, 6}, {6, 7, 9}, {9, 7, 10}, {9, 8, 11}, {10, 8, 11}, {11, 8, 10},
}

func constructTemporalStructureHoldoutMultiCompilations(seed string, anchors []temporalStructureHoldoutSelectedAnchor) ([]TemporalStructureChallengeCase, []TemporalStructureHoldoutMultiCompilation, error) {
	if len(anchors) != temporalStructureHoldoutClassCases {
		return nil, nil, fmt.Errorf("temporal structure multi-item holdout requires twelve anchors")
	}
	ordered := append([]temporalStructureHoldoutSelectedAnchor(nil), anchors...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].receipt.Role != ordered[j].receipt.Role {
			return ordered[i].receipt.Role < ordered[j].receipt.Role
		}
		return ordered[i].receipt.RankSHA256 < ordered[j].receipt.RankSHA256
	})

	cases := make([]TemporalStructureChallengeCase, 0, len(temporalStructureMultiSchedule))
	receipts := make([]TemporalStructureHoldoutMultiCompilation, 0, len(temporalStructureMultiSchedule))
	for scheduleIndex, indexes := range temporalStructureMultiSchedule {
		selected := []temporalStructureHoldoutSelectedAnchor{ordered[indexes[0]], ordered[indexes[1]], ordered[indexes[2]]}
		var durationMS int64
		var sourceIDs []string
		var roles []fillereval.TemporalRole
		var joins []int64
		var segments []TemporalStructureChallengeSegment
		for index, anchor := range selected {
			if index > 0 {
				joins = append(joins, durationMS)
			}
			sourceIDs = append(sourceIDs, anchor.source.ID)
			roles = append(roles, anchor.receipt.Role)
			segments = append(segments, TemporalStructureChallengeSegment{SourceID: anchor.source.ID, DurationMS: anchor.source.DurationMS})
			durationMS += anchor.source.DurationMS
		}
		trait := temporalStructureMultiTrait(roles)
		if scheduleIndex < len(temporalStructureMultiSchedule)/2 && trait != temporalStructureMultiSameRoleJoin || scheduleIndex >= len(temporalStructureMultiSchedule)/2 && trait != temporalStructureMultiMixedRoleJoins {
			return nil, nil, fmt.Errorf("temporal structure multi-item schedule does not satisfy its declared join traits")
		}
		caseID := temporalStructureHoldoutCaseID(seed, "multi_compilation", strings.Join(sourceIDs, "\x00"))
		cases = append(cases, TemporalStructureChallengeCase{
			ID: caseID, Unit: fillereval.UnitCompilation,
			Slices: []string{trait, TemporalStructureSliceThreeItemCompilation}, Segments: segments,
		})
		receipts = append(receipts, TemporalStructureHoldoutMultiCompilation{
			CaseID: caseID, SourceIDs: sourceIDs, Roles: roles, JoinTimesMS: joins, DurationMS: durationMS, Trait: trait,
		})
	}
	return cases, receipts, nil
}

func temporalStructureMultiTrait(roles []fillereval.TemporalRole) string {
	for index := 1; index < len(roles); index++ {
		if roles[index-1] == roles[index] {
			return temporalStructureMultiSameRoleJoin
		}
	}
	return temporalStructureMultiMixedRoleJoins
}
