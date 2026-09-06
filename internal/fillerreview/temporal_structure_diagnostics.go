package fillerreview

import (
	"fmt"
	"slices"
	"sort"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func buildTemporalStructurePairSummaries(cases []TemporalStructureCaseComparison, assessments []temporalStructureLoadedAssessment) []TemporalStructurePairSummary {
	var summaries []TemporalStructurePairSummary
	for left := 0; left < len(assessments); left++ {
		for right := left + 1; right < len(assessments); right++ {
			leftID, rightID := assessments[left].set.Assessor.ID, assessments[right].set.Assessor.ID
			summary := TemporalStructurePairSummary{Pair: leftID + "__" + rightID, Cases: len(cases)}
			for _, item := range cases {
				first := temporalStructureCaseResult(item.Assessments, leftID)
				second := temporalStructureCaseResult(item.Assessments, rightID)
				if first.Prediction.Failure != "" || second.Prediction.Failure != "" {
					continue
				}
				summary.OperationallyComparable++
				if first.Prediction.Unit == second.Prediction.Unit {
					summary.ExactUnitAgreement++
				}
				if (first.Prediction.Unit == fillereval.UnitStandalone) == (second.Prediction.Unit == fillereval.UnitStandalone) {
					summary.StandaloneClassAgreement++
				}
				if first.Prediction.Unit == fillereval.UnitStandalone && second.Prediction.Unit == fillereval.UnitStandalone {
					summary.RoleComparable++
					if first.Prediction.Role == second.Prediction.Role {
						summary.RoleAgreement++
					}
				}
				if first.Prediction.Unit == second.Prediction.Unit && (first.Prediction.Unit != fillereval.UnitStandalone || first.Prediction.Role == second.Prediction.Role) {
					summary.ExactLabelAgreement++
				}
				if slices.Equal(first.Prediction.Segments, second.Prediction.Segments) {
					summary.ExactSegmentPlanAgreement++
				}
			}
			summaries = append(summaries, summary)
		}
	}
	return summaries
}

func temporalStructureDiagnosticReasons(item TemporalStructureCaseComparison) []string {
	reasons := make(map[string]struct{})
	units := make(map[fillereval.UnitKind]struct{})
	roles := make(map[fillereval.TemporalRole]struct{})
	for _, assessment := range item.Assessments {
		if assessment.Prediction.Failure != "" {
			reasons["operational_failure"] = struct{}{}
			continue
		}
		units[assessment.Prediction.Unit] = struct{}{}
		if assessment.Prediction.Unit == fillereval.UnitStandalone {
			roles[assessment.Prediction.Role] = struct{}{}
		}
		if !assessment.UnitCorrect {
			reasons["unit_error"] = struct{}{}
		}
		if assessment.RoleComparable && !assessment.RoleCorrect {
			reasons["role_error"] = struct{}{}
		}
		if assessment.UnderSplits > 0 {
			reasons["under_split"] = struct{}{}
		}
		if assessment.OverSplits > 0 {
			reasons["over_split"] = struct{}{}
		}
		if assessment.SegmentRoleCorrect != assessment.SegmentRoleTargets {
			reasons["segment_role_error"] = struct{}{}
		}
		for _, distance := range assessment.BoundaryDistances {
			if !distance.Within5000MS {
				reasons["boundary_over_5000ms"] = struct{}{}
			}
		}
	}
	if len(units) > 1 {
		reasons["model_unit_disagreement"] = struct{}{}
	}
	if len(roles) > 1 {
		reasons["model_role_disagreement"] = struct{}{}
	}
	result := make([]string, 0, len(reasons))
	for reason := range reasons {
		result = append(result, reason)
	}
	sort.Strings(result)
	return result
}

func temporalStructureComparisonDisposition(candidates []TemporalStructureDiagnosticCandidate) TemporalStructureComparisonDisposition {
	disposition := TemporalStructureComparisonDisposition{BlindHumanAuditRequired: false, TrainingAllowed: false}
	if len(candidates) == 0 {
		disposition.NextAction = "expand_provenance_grounded_challenge"
		return disposition
	}
	disposition.NextAction = "inspect_targeted_diagnostics"
	for _, candidate := range candidates {
		disposition.TargetedCases = append(disposition.TargetedCases, candidate.Alias)
	}
	return disposition
}

func temporalStructureCaseResult(items []TemporalStructureAssessorCaseResult, assessorID string) TemporalStructureAssessorCaseResult {
	for _, item := range items {
		if item.AssessorID == assessorID {
			return item
		}
	}
	panic(fmt.Sprintf("validated temporal structure assessor %q disappeared", assessorID))
}
