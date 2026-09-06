package fillerreview

import (
	"slices"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

type temporalStructureCertificationAccumulator struct {
	cases        int
	decided      int
	held         int
	wrong        int
	failureCodes []string
}

func scoreTemporalStructureCertification(decision TemporalStructureDecisionReport, manifest TemporalStructureChallengeManifest, authority TemporalStructureChallengeAuthority, certifiedAt time.Time) TemporalStructureCertificationReport {
	report := TemporalStructureCertificationReport{
		SchemaVersion: TemporalStructureCertificationSchemaVersion, ContractVersion: TemporalStructureCertificationContractVersion,
		CertifiedAt: certifiedAt.UTC(), ChallengeID: decision.ChallengeID, Cases: len(authority.Cases),
		AssessmentMediaProfileSHA256: authority.AssessmentMediaProfile.SHA256,
		BoundaryToleranceMS:          TemporalStructureNearBoundaryMS,
		MinimumDecidedCases:          temporalStructureCertificationMinimumDecidedCases,
		MinimumUnitDecisions:         temporalStructureCertificationMinimumUnitDecisions,
		MinimumSliceDecisions:        temporalStructureCertificationMinimumSliceDecisions,
		TrainingAllowed:              false, ProductionAdmissionAllowed: false,
	}
	for _, assessor := range decision.Assessors {
		report.AssessorIDs = append(report.AssessorIDs, assessor.Assessor.ID)
		report.ModelFamilies = append(report.ModelFamilies, assessor.Assessor.ModelFamily)
	}
	report.AssessorIDs = sortedUniqueStrings(report.AssessorIDs)
	report.ModelFamilies = sortedUniqueStrings(report.ModelFamilies)
	for index, item := range manifest.Cases {
		if index == 0 || item.Video.DurationMS < report.MinimumTimelineDurationMS {
			report.MinimumTimelineDurationMS = item.Video.DurationMS
		}
		if item.Video.DurationMS > report.MaximumTimelineDurationMS {
			report.MaximumTimelineDurationMS = item.Video.DurationMS
		}
		if item.Video.Bytes > report.MaximumAssessmentMediaBytes {
			report.MaximumAssessmentMediaBytes = item.Video.Bytes
		}
	}
	if report.Cases < temporalStructureCertificationMinimumCases {
		report.FailureCodes = append(report.FailureCodes, "insufficient_cases")
	}
	if len(report.ModelFamilies) < 2 || decision.IndependentModelFamilies < 2 {
		report.FailureCodes = append(report.FailureCodes, "insufficient_independent_model_families")
	}

	decisionByAlias := make(map[string]TemporalStructureCaseDecision, len(decision.Decisions))
	for _, item := range decision.Decisions {
		if _, duplicate := decisionByAlias[item.Alias]; duplicate {
			report.FailureCodes = append(report.FailureCodes, "duplicate_decision_case")
		}
		decisionByAlias[item.Alias] = item
	}
	unitMetrics := make(map[fillereval.UnitKind]*temporalStructureCertificationAccumulator)
	for _, unit := range temporalStructureScoredUnits() {
		unitMetrics[unit] = &temporalStructureCertificationAccumulator{}
	}
	sliceMetrics := make(map[string]*temporalStructureCertificationAccumulator)
	for _, slice := range temporalStructureCertificationRequiredSlices {
		sliceMetrics[slice] = &temporalStructureCertificationAccumulator{}
	}
	for _, truth := range authority.Cases {
		unitMetric := unitMetrics[truth.Unit]
		unitMetric.cases++
		for _, slice := range truth.Slices {
			if metric := sliceMetrics[slice]; metric != nil {
				metric.cases++
			}
		}
		candidate, exists := decisionByAlias[truth.Alias]
		if !exists {
			report.FailureCodes = append(report.FailureCodes, "missing_decision_case")
			unitMetric.held++
			for _, slice := range truth.Slices {
				if metric := sliceMetrics[slice]; metric != nil {
					metric.held++
				}
			}
			continue
		}
		if candidate.Status != TemporalStructureDecisionConfirmed {
			report.HeldCases++
			unitMetric.held++
			for _, slice := range truth.Slices {
				if metric := sliceMetrics[slice]; metric != nil {
					metric.held++
				}
			}
			continue
		}
		report.DecidedCases++
		unitMetric.decided++
		for _, slice := range truth.Slices {
			if metric := sliceMetrics[slice]; metric != nil {
				metric.decided++
			}
		}
		publicCase := temporalStructurePublicCase(manifest, truth.Alias)
		failures := temporalStructureAutomaticDecisionFailures(candidate, truth, publicCase.Video.DurationMS)
		if len(failures) == 0 {
			continue
		}
		report.WrongAutomaticDecisions++
		report.FailureCodes = append(report.FailureCodes, "wrong_automatic_decision")
		report.FailureCodes = append(report.FailureCodes, failures...)
		unitMetric.wrong++
		unitMetric.failureCodes = append(unitMetric.failureCodes, failures...)
		for _, slice := range truth.Slices {
			if metric := sliceMetrics[slice]; metric != nil {
				metric.wrong++
				metric.failureCodes = append(metric.failureCodes, failures...)
			}
		}
	}
	if len(decisionByAlias) != len(authority.Cases) {
		report.FailureCodes = append(report.FailureCodes, "decision_case_set")
	}
	if report.DecidedCases < temporalStructureCertificationMinimumDecidedCases {
		report.FailureCodes = append(report.FailureCodes, "insufficient_decided_cases")
	}
	report.FailureCodes = sortedUniqueStrings(report.FailureCodes)

	for _, unit := range temporalStructureScoredUnits() {
		metric := unitMetrics[unit]
		result := TemporalStructureUnitCertification{
			Unit: unit, Cases: metric.cases, DecidedCases: metric.decided, HeldCases: metric.held,
			WrongAutomaticDecisions: metric.wrong, FailureCodes: sortedUniqueStrings(metric.failureCodes),
		}
		if result.DecidedCases < temporalStructureCertificationMinimumUnitDecisions {
			result.FailureCodes = sortedUniqueStrings(append(result.FailureCodes, "insufficient_unit_decisions"))
		}
		result.Passed = len(report.FailureCodes) == 0 && len(result.FailureCodes) == 0
		if result.Passed {
			report.CertifiedUnits = append(report.CertifiedUnits, unit)
		}
		report.Units = append(report.Units, result)
	}
	for _, slice := range temporalStructureCertificationRequiredSlices {
		metric := sliceMetrics[slice]
		result := TemporalStructureSliceCertification{
			Slice: slice, Cases: metric.cases, DecidedCases: metric.decided, HeldCases: metric.held,
			WrongAutomaticDecisions: metric.wrong, FailureCodes: sortedUniqueStrings(metric.failureCodes),
		}
		if result.Cases < temporalStructureCertificationMinimumSliceDecisions {
			result.FailureCodes = append(result.FailureCodes, "insufficient_slice_cases")
		}
		if result.DecidedCases < temporalStructureCertificationMinimumSliceDecisions {
			result.FailureCodes = append(result.FailureCodes, "insufficient_slice_decisions")
		}
		result.FailureCodes = sortedUniqueStrings(result.FailureCodes)
		result.Passed = len(report.FailureCodes) == 0 && len(result.FailureCodes) == 0
		if result.Passed {
			report.CertifiedSlices = append(report.CertifiedSlices, slice)
		}
		report.Slices = append(report.Slices, result)
	}
	if len(report.CertifiedUnits) == len(temporalStructureScoredUnits()) && len(report.CertifiedSlices) == len(temporalStructureCertificationRequiredSlices) {
		report.CertificationStatus = TemporalStructureCertificationPassed
		report.NextAction = "run_locked_reducer_shadow"
	} else {
		report.CertificationStatus = TemporalStructureCertificationFailed
		report.NextAction = "diagnose_held_and_wrong_reducer_decisions"
	}
	return report
}

func temporalStructureAutomaticDecisionFailures(decision TemporalStructureCaseDecision, truth TemporalStructureChallengeAuthorityCase, durationMS int64) []string {
	var failures []string
	if decision.Unit != truth.Unit {
		failures = append(failures, "unit_error")
	}
	if truth.Unit == fillereval.UnitStandalone && decision.Role != truth.Role {
		failures = append(failures, "role_error")
	}
	result := TemporalStructureAssessorCaseResult{Prediction: TemporalStructurePredictedLabel{Unit: decision.Unit, Role: decision.Role}}
	for _, segment := range decision.Segments {
		result.Prediction.Segments = append(result.Prediction.Segments, TemporalStructurePredictedSegment{StartMS: segment.StartMS, EndMS: segment.EndMS, Role: segment.Role})
		if segment.Disposition != temporalStructureDecisionDisposition(segment.Role) {
			failures = append(failures, "segment_disposition_error")
		}
	}
	scoreTemporalStructureSegments(&result, temporalStructureTruthSegments(truth, durationMS))
	if !result.CoverageComplete {
		failures = append(failures, "timeline_gap")
	}
	if result.UnderSplits != 0 {
		failures = append(failures, "under_split")
	}
	if result.OverSplits != 0 {
		failures = append(failures, "over_split")
	}
	if result.SegmentRoleCorrect != result.SegmentRoleTargets {
		failures = append(failures, "segment_role_error")
	}
	return sortedUniqueStrings(failures)
}

func sortedUniqueStrings(values []string) []string {
	sort.Strings(values)
	return slices.Compact(values)
}
