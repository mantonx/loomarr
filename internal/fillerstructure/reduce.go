package fillerstructure

import (
	"slices"
	"sort"
	"strings"
)

func Reduce(request Request) Decision {
	candidates := slices.Clone(request.Candidates)
	for index := range candidates {
		candidates[index].Assessor.ModelFamily = strings.ToLower(strings.TrimSpace(candidates[index].Assessor.ModelFamily))
		candidates[index].Segments = slices.Clone(candidates[index].Segments)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Assessor.ID != candidates[j].Assessor.ID {
			return candidates[i].Assessor.ID < candidates[j].Assessor.ID
		}
		return candidates[i].Assessor.AssessmentSHA256 < candidates[j].Assessor.AssessmentSHA256
	})
	decision := Decision{Source: request.Source, Input: request.Input, Status: StatusHeld, Candidates: candidates}
	if invalidCandidates(Request{Source: request.Source, Input: request.Input, BoundaryToleranceMS: request.BoundaryToleranceMS, Candidates: candidates}) {
		decision.ReasonCodes = []string{ReasonInvalidCandidate}
		return decision
	}
	reasons := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.Failure != "" {
			reasons[ReasonOperationalFailure] = struct{}{}
		}
	}
	if len(reasons) != 0 {
		decision.ReasonCodes = sortedReasons(reasons)
		return decision
	}

	unit := candidates[0].Unit
	for _, candidate := range candidates[1:] {
		if candidate.Unit != unit {
			reasons[ReasonUnitDisagreement] = struct{}{}
		}
	}
	if unit == UnitUnclear || unit == UnitUnusable {
		reasons[ReasonUnsupportedUnit] = struct{}{}
	}
	role := Role("")
	if unit == UnitStandalone {
		role = candidates[0].Role
		for _, candidate := range candidates[1:] {
			if candidate.Role != role {
				reasons[ReasonRoleDisagreement] = struct{}{}
			}
		}
	}
	intervals := len(candidates[0].Segments)
	for _, candidate := range candidates[1:] {
		if len(candidate.Segments) != intervals {
			reasons[ReasonIntervalCount] = struct{}{}
		}
	}
	if len(reasons) != 0 {
		decision.ReasonCodes = sortedReasons(reasons)
		return decision
	}

	for index := range intervals {
		segmentRole := candidates[0].Segments[index].Role
		if segmentRole == RoleAmbiguous || segmentRole == RoleUnusable {
			reasons[ReasonUnresolvedInterval] = struct{}{}
		}
		for _, candidate := range candidates[1:] {
			if candidate.Segments[index].Role != segmentRole {
				reasons[ReasonIntervalRole] = struct{}{}
			}
			if candidate.Segments[index].Role == RoleAmbiguous || candidate.Segments[index].Role == RoleUnusable {
				reasons[ReasonUnresolvedInterval] = struct{}{}
			}
		}
	}
	if len(reasons) != 0 {
		decision.ReasonCodes = sortedReasons(reasons)
		return decision
	}

	boundaries := []int64{0}
	for index := 0; index < intervals-1; index++ {
		byFamily := make(map[string][]int64)
		for _, candidate := range candidates {
			family := strings.ToLower(strings.TrimSpace(candidate.Assessor.ModelFamily))
			byFamily[family] = append(byFamily[family], candidate.Segments[index].EndMS)
		}
		familyBoundaries := make([]int64, 0, len(byFamily))
		for _, values := range byFamily {
			minimum, maximum := valueRange(values)
			if maximum-minimum > request.BoundaryToleranceMS {
				reasons[ReasonBoundary] = struct{}{}
			}
			familyBoundaries = append(familyBoundaries, minimum+(maximum-minimum)/2)
		}
		minimum, maximum := valueRange(familyBoundaries)
		if maximum-minimum > request.BoundaryToleranceMS {
			reasons[ReasonBoundary] = struct{}{}
		}
		boundaries = append(boundaries, minimum+(maximum-minimum)/2)
	}
	boundaries = append(boundaries, request.Source.DurationMS)
	if len(reasons) != 0 {
		decision.ReasonCodes = sortedReasons(reasons)
		return decision
	}

	decision.Status = StatusConfirmed
	decision.ReasonCodes = []string{ReasonAgreement}
	decision.Unit, decision.Role = unit, role
	for index := range intervals {
		segmentRole := candidates[0].Segments[index].Role
		decision.Segments = append(decision.Segments, DecisionSegment{
			StartMS: boundaries[index], EndMS: boundaries[index+1],
			Disposition: DispositionForRole(segmentRole), Role: segmentRole,
		})
	}
	return decision
}

// DispositionForRole projects one interval role without weakening unresolved roles.
func DispositionForRole(role Role) Disposition {
	if fillerRole(role) {
		return DispositionFillerCandidate
	}
	if role == RoleProgrammeFragment || role == RoleNonFiller {
		return DispositionNonFiller
	}
	return DispositionUnresolved
}

func valueRange(values []int64) (int64, int64) {
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		minimum, maximum = min(minimum, value), max(maximum, value)
	}
	return minimum, maximum
}

func sortedReasons(reasons map[string]struct{}) []string {
	result := make([]string, 0, len(reasons))
	for reason := range reasons {
		result = append(result, reason)
	}
	sort.Strings(result)
	return result
}
