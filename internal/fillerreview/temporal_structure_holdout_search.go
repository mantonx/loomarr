package fillerreview

import (
	"sort"

	"github.com/loomarr/loomarr/internal/fillereval"
)

type temporalStructureHoldoutAnchorSearch struct {
	anchors    []temporalStructureHoldoutSelectedAnchor
	byRole     map[fillereval.TemporalRole][]int
	roles      []fillereval.TemporalRole
	candidates map[string][]temporalStructureHoldoutCompilationPair
	bandKnown  map[string]map[uint64]struct{}
	bandResult map[string]map[uint64]bool
	selected   uint64
	families   map[string]struct{}
	sources    map[string]struct{}
}

// solveTemporalStructureHoldoutAnchors precomputes every seed-ranked ordered
// pair once, then searches the small role-quota product with bit masks. This
// keeps the joint search deterministic without re-hashing the same pair for
// each possible 12-anchor set.
func solveTemporalStructureHoldoutAnchors(seed string, anchors []temporalStructureHoldoutSelectedAnchor) ([]temporalStructureHoldoutSelectedAnchor, bool) {
	if len(anchors) > 64 {
		return nil, false
	}
	search := &temporalStructureHoldoutAnchorSearch{
		anchors: anchors,
		roles: []fillereval.TemporalRole{
			fillereval.TemporalRoleBumper, fillereval.TemporalRoleCommercial, fillereval.TemporalRolePromo,
			fillereval.TemporalRolePSA, fillereval.TemporalRoleTrailer,
		},
		byRole: map[fillereval.TemporalRole][]int{}, candidates: map[string][]temporalStructureHoldoutCompilationPair{},
		bandKnown: map[string]map[uint64]struct{}{}, bandResult: map[string]map[uint64]bool{},
		families: map[string]struct{}{}, sources: map[string]struct{}{},
	}
	for _, band := range []string{"early", "middle", "late"} {
		search.candidates[band] = temporalStructureHoldoutPairCandidates(seed, anchors, band)
		search.bandKnown[band], search.bandResult[band] = map[uint64]struct{}{}, map[uint64]bool{}
	}
	scores := temporalStructureHoldoutAnchorCoverageScores(anchors, search.candidates)
	for index, anchor := range anchors {
		search.byRole[anchor.receipt.Role] = append(search.byRole[anchor.receipt.Role], index)
	}
	for _, role := range search.roles {
		sort.Slice(search.byRole[role], func(i, j int) bool {
			left, right := search.byRole[role][i], search.byRole[role][j]
			return scores[left] > scores[right] || scores[left] == scores[right] && anchors[left].receipt.CaseID < anchors[right].receipt.CaseID
		})
	}
	indices, ok := search.chooseRole(0)
	if !ok {
		return nil, false
	}
	selected := make([]temporalStructureHoldoutSelectedAnchor, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, anchors[index])
	}
	return selected, true
}

func temporalStructureHoldoutAnchorCoverageScores(anchors []temporalStructureHoldoutSelectedAnchor, candidates map[string][]temporalStructureHoldoutCompilationPair) []int {
	scores := make([]int, len(anchors))
	for _, band := range []string{"early", "middle", "late"} {
		groups := map[string]int{}
		for _, candidate := range candidates[band] {
			groups[temporalStructureHoldoutPairCoverageGroup(candidate)]++
		}
		for _, candidate := range candidates[band] {
			weight := 1_000_000 / groups[temporalStructureHoldoutPairCoverageGroup(candidate)]
			scores[candidate.first] += weight
			scores[candidate.second] += weight
		}
	}
	return scores
}

func temporalStructureHoldoutPairCoverageGroup(candidate temporalStructureHoldoutCompilationPair) string {
	roleGroup := "cross"
	if candidate.sameRole {
		roleGroup = "same"
	}
	return candidate.band + "\x00" + string(candidate.stratum) + "\x00" + roleGroup
}

func (search *temporalStructureHoldoutAnchorSearch) chooseRole(roleIndex int) ([]int, bool) {
	if roleIndex == len(search.roles) {
		for _, band := range []string{"early", "middle", "late"} {
			if !search.selectedMaskSatisfiesBand(band) {
				return nil, false
			}
		}
		var result []int
		for index := range search.anchors {
			if search.selected&(uint64(1)<<index) != 0 {
				result = append(result, index)
			}
		}
		return result, true
	}
	role := search.roles[roleIndex]
	return search.chooseWithinRole(roleIndex, search.byRole[role], 0, temporalStructureHoldoutRoleQuotas[role])
}

func (search *temporalStructureHoldoutAnchorSearch) selectedMaskSatisfiesBand(band string) bool {
	indices := make([]int, 0, temporalStructureHoldoutClassCases)
	for index := range search.anchors {
		if search.selected&(uint64(1)<<index) != 0 {
			indices = append(indices, index)
		}
	}
	return search.chooseBandSourceMask(band, indices, 0, 8, 0)
}

func (search *temporalStructureHoldoutAnchorSearch) chooseBandSourceMask(band string, indices []int, start, remaining int, mask uint64) bool {
	if remaining == 0 {
		if _, known := search.bandKnown[band][mask]; known {
			return search.bandResult[band][mask]
		}
		result := temporalStructureHoldoutExactMaskSatisfiesBand(search.candidates[band], mask, 0, 0, 0, 0, map[TemporalTransitionStratum]int{})
		search.bandKnown[band][mask] = struct{}{}
		search.bandResult[band][mask] = result
		return result
	}
	if len(indices)-start < remaining {
		return false
	}
	for position := start; position < len(indices); position++ {
		if search.chooseBandSourceMask(band, indices, position+1, remaining-1, mask|uint64(1)<<indices[position]) {
			return true
		}
	}
	return false
}

func (search *temporalStructureHoldoutAnchorSearch) chooseWithinRole(roleIndex int, candidates []int, start, remaining int) ([]int, bool) {
	if remaining == 0 {
		return search.chooseRole(roleIndex + 1)
	}
	if len(candidates)-start < remaining {
		return nil, false
	}
	for position := start; position < len(candidates); position++ {
		index := candidates[position]
		anchor := search.anchors[index]
		if _, duplicate := search.families[anchor.receipt.FamilyID]; duplicate {
			continue
		}
		if _, duplicate := search.sources[anchor.source.SHA256]; duplicate {
			continue
		}
		search.selected |= uint64(1) << index
		search.families[anchor.receipt.FamilyID], search.sources[anchor.source.SHA256] = struct{}{}, struct{}{}
		if result, ok := search.chooseWithinRole(roleIndex, candidates, position+1, remaining-1); ok {
			return result, true
		}
		search.selected &^= uint64(1) << index
		delete(search.families, anchor.receipt.FamilyID)
		delete(search.sources, anchor.source.SHA256)
	}
	return nil, false
}

func temporalStructureHoldoutExactMaskSatisfiesBand(candidates []temporalStructureHoldoutCompilationPair, target, used uint64, start, chosen, sameRoles int, strata map[TemporalTransitionStratum]int) bool {
	if chosen == 4 {
		return used == target && sameRoles == 2 && strata[TemporalTransitionBlackBoundary] > 0 && strata[TemporalTransitionAudibleNonblackCut] > 0 && strata[TemporalTransitionSilenceTouchedNonblackCut] > 0
	}
	if sameRoles > 2 || sameRoles+(4-chosen) < 2 {
		return false
	}
	missingStrata := 0
	for _, stratum := range []TemporalTransitionStratum{TemporalTransitionBlackBoundary, TemporalTransitionAudibleNonblackCut, TemporalTransitionSilenceTouchedNonblackCut} {
		if strata[stratum] == 0 {
			missingStrata++
		}
	}
	if missingStrata > 4-chosen {
		return false
	}
	for index := start; index < len(candidates); index++ {
		candidate := candidates[index]
		pairMask := uint64(1)<<candidate.first | uint64(1)<<candidate.second
		if target&pairMask != pairMask || used&pairMask != 0 || strata[candidate.stratum] == 2 {
			continue
		}
		strata[candidate.stratum]++
		nextSameRoles := sameRoles
		if candidate.sameRole {
			nextSameRoles++
		}
		if temporalStructureHoldoutExactMaskSatisfiesBand(candidates, target, used|pairMask, index+1, chosen+1, nextSameRoles, strata) {
			return true
		}
		strata[candidate.stratum]--
	}
	return false
}
