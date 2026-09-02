package fillersafetycert

import (
	"fmt"
	"strings"
)

func validateReport(report Report) error {
	if report.SchemaVersion != SchemaVersion || report.ContractVersion != ContractVersion || report.ScoredAt.IsZero() ||
		!validSHA256(report.AuthoritySHA256) || !validSHA256(report.ResultManifestSHA256) ||
		!validSHA256(report.PolicySHA256) || !validSHA256(report.ProposerSHA256) || !boundedID(report.Implementation) ||
		(report.ChallengeKind != ChallengeDevelopment && report.ChallengeKind != ChallengeCertification) ||
		report.PositiveFamilies < MinimumPositiveFamilies || report.PositiveSources != report.PositiveFamilies ||
		report.PositiveIntervals <= 0 || report.CleanSources <= 0 ||
		(report.CertificationStatus != StatusPassed && report.CertificationStatus != StatusDiagnosticPassed && report.CertificationStatus != StatusFailed) ||
		report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed ||
		report.NextAction != NextAction {
		return fmt.Errorf("report identity, counts, permissions, or status is invalid")
	}
	if report.DetectedPositiveSources+report.MissedPositiveSources != report.PositiveSources ||
		report.DetectedPositiveIntervals > report.PositiveIntervals ||
		report.SourceRecall != float64(report.DetectedPositiveSources)/float64(report.PositiveFamilies) ||
		report.SourceRecallExactLower95 != exactLower95(report.DetectedPositiveSources, report.PositiveFamilies) ||
		report.CleanFalsePositiveSources > report.CleanSources || len(report.Cases) != report.PositiveSources+report.CleanSources ||
		len(report.CleanSlices) == 0 {
		return fmt.Errorf("report summary is inconsistent")
	}
	positive, detected, missed, clean, falsePositive, holds, intervals, detectedIntervals := 0, 0, 0, 0, 0, 0, 0, 0
	previousAlias := ""
	for _, item := range report.Cases {
		if !validOpaqueID(item.Alias, "sc-") || item.Alias <= previousAlias {
			return fmt.Errorf("report cases are invalid or disordered")
		}
		previousAlias = item.Alias
		switch item.Label {
		case LabelPositive:
			positive++
			if item.PositiveIntervals <= 0 || item.DetectedPositiveIntervals > item.PositiveIntervals {
				return fmt.Errorf("positive case interval counts are invalid")
			}
			intervals += item.PositiveIntervals
			detectedIntervals += item.DetectedPositiveIntervals
			switch item.Outcome {
			case OutcomeDetected:
				detected++
			case OutcomeMissed:
				missed++
			case OutcomeCoverageHold:
				missed++
				holds++
			default:
				return fmt.Errorf("positive case outcome is invalid")
			}
		case LabelClean:
			clean++
			if item.PositiveIntervals != 0 || item.DetectedPositiveIntervals != 0 {
				return fmt.Errorf("clean case contains interval counts")
			}
			switch item.Outcome {
			case OutcomeClean:
			case OutcomeFalsePositive:
				falsePositive++
			case OutcomeCoverageHold:
				holds++
			default:
				return fmt.Errorf("clean case outcome is invalid")
			}
		default:
			return fmt.Errorf("case label is invalid")
		}
	}
	if positive != report.PositiveSources || detected != report.DetectedPositiveSources || missed != report.MissedPositiveSources ||
		clean != report.CleanSources || falsePositive != report.CleanFalsePositiveSources || holds != report.CoverageHolds ||
		intervals != report.PositiveIntervals || detectedIntervals != report.DetectedPositiveIntervals {
		return fmt.Errorf("report cases do not reproduce its summary")
	}
	allCleanPassed := true
	previousSlice := ""
	for _, metric := range report.CleanSlices {
		if strings.TrimSpace(metric.Slice) == "" || metric.Slice <= previousSlice || metric.CleanSources <= 0 ||
			metric.FalsePositives < 0 || metric.FalsePositives > metric.CleanSources ||
			metric.FalsePositiveRate != float64(metric.FalsePositives)/float64(metric.CleanSources) ||
			metric.Passed != (metric.FalsePositiveRate <= MaximumCleanFPRate) {
			return fmt.Errorf("clean slice is invalid or disordered")
		}
		previousSlice = metric.Slice
		allCleanPassed = allCleanPassed && metric.Passed
	}
	wantPassed := report.MissedPositiveSources == 0 && report.SourceRecallExactLower95 >= 0.95 &&
		report.CoverageHolds == 0 && allCleanPassed
	wantStatus := StatusFailed
	if wantPassed && report.ChallengeKind == ChallengeCertification {
		wantStatus = StatusPassed
	} else if wantPassed {
		wantStatus = StatusDiagnosticPassed
	}
	if report.CertificationStatus != wantStatus {
		return fmt.Errorf("report status does not reproduce")
	}
	return nil
}
