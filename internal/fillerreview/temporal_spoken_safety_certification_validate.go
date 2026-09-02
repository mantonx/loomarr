package fillerreview

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

func validateTemporalSpokenSafetyChallengeCases(cases []TemporalSpokenSafetyChallengeAuthorityCase, projection TemporalSpokenSafetyReport) error {
	sources := make(map[string]TemporalSpokenSafetySourceDisposition, len(projection.SourceDispositions))
	for _, source := range projection.SourceDispositions {
		if _, duplicate := sources[source.SourceSHA256]; duplicate {
			return fmt.Errorf("spoken-safety projection repeats source content authority")
		}
		sources[source.SourceSHA256] = source
	}
	seenAliases, seenSources, seenFamilies := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	positiveFamilies := 0
	positiveSlices, cleanSlices := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range cases {
		source, exists := sources[item.SourceSHA256]
		if !exists || !validTemporalSpokenSafetyChallengeAlias(item.Alias) || !validTemporalSpokenSafetyFamilyID(item.SourceFamilyID) || !validTemporalSpokenSafetyLocale(item.Locale) || len(item.Slices) == 0 || len(item.Slices) > 8 || !strictlySortedStrings(item.Slices) {
			return fmt.Errorf("spoken-safety challenge case has invalid or unbound authority")
		}
		if _, duplicate := seenAliases[item.Alias]; duplicate {
			return fmt.Errorf("spoken-safety challenge repeats an alias")
		}
		if _, duplicate := seenSources[item.SourceSHA256]; duplicate {
			return fmt.Errorf("spoken-safety challenge repeats a source")
		}
		if _, duplicate := seenFamilies[item.SourceFamilyID]; duplicate {
			return fmt.Errorf("spoken-safety challenge is not source-family-disjoint")
		}
		seenAliases[item.Alias], seenSources[item.SourceSHA256], seenFamilies[item.SourceFamilyID] = struct{}{}, struct{}{}, struct{}{}
		for _, slice := range item.Slices {
			if !validTemporalSpokenSafetyChallengeSlice(item.Label, slice) {
				return fmt.Errorf("spoken-safety challenge case has an unknown label slice")
			}
			if item.Label == TemporalSpokenSafetyChallengeClean {
				cleanSlices[slice] = struct{}{}
			} else {
				positiveSlices[slice] = struct{}{}
			}
		}
		switch item.Label {
		case TemporalSpokenSafetyChallengePositive:
			positiveFamilies++
			if len(item.PositiveIntervals) == 0 || !validTemporalSpokenSafetyPositiveIntervals(item.PositiveIntervals, source.SourceDurationMS) {
				return fmt.Errorf("spoken-safety positive challenge case has invalid intervals")
			}
		case TemporalSpokenSafetyChallengeClean:
			if len(item.PositiveIntervals) != 0 {
				return fmt.Errorf("spoken-safety clean challenge case contains positive intervals")
			}
		default:
			return fmt.Errorf("spoken-safety challenge case has an unknown label")
		}
	}
	if positiveFamilies < temporalSpokenSafetyMinimumPositiveFamilies || !temporalSpokenSafetyCoversSlices(positiveSlices, temporalSpokenSafetyRequiredPositiveSlices()) || !temporalSpokenSafetyCoversSlices(cleanSlices, temporalSpokenSafetyRequiredCleanSlices()) {
		return fmt.Errorf("spoken-safety challenge requires at least %d positive families and every declared positive and clean slice", temporalSpokenSafetyMinimumPositiveFamilies)
	}
	return nil
}

func validTemporalSpokenSafetyPositiveIntervals(intervals []TemporalSpokenSafetyPositiveInterval, durationMS int64) bool {
	var previousEnd int64
	for index, interval := range intervals {
		if !validTemporalSpokenSafetyRuleID(interval.RuleID) || interval.StartMS < 0 || interval.EndMS <= interval.StartMS || interval.EndMS > durationMS || index > 0 && interval.StartMS < previousEnd {
			return false
		}
		previousEnd = interval.EndMS
	}
	return true
}

func validTemporalSpokenSafetyChallengeAlias(value string) bool {
	return validTemporalSpokenSafetyOpaqueID(value, "sc-")
}

func validTemporalSpokenSafetyFamilyID(value string) bool {
	return validTemporalSpokenSafetyOpaqueID(value, "family-")
}

func validTemporalSpokenSafetyOpaqueID(value, prefix string) bool {
	if len(value) != len(prefix)+24 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, r := range value[len(prefix):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validTemporalSpokenSafetyLocale(value string) bool {
	if len(value) < 2 || len(value) > 35 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func validTemporalSpokenSafetyChallengeSlice(label, value string) bool {
	positive := temporalSpokenSafetyRequiredPositiveSlices()
	clean := temporalSpokenSafetyRequiredCleanSlices()
	return label == TemporalSpokenSafetyChallengePositive && slices.Contains(positive, value) || label == TemporalSpokenSafetyChallengeClean && slices.Contains(clean, value)
}

func temporalSpokenSafetyRequiredPositiveSlices() []string {
	return []string{TemporalSpokenSafetySliceQuietSpeech, TemporalSpokenSafetySliceMusicOverlap, TemporalSpokenSafetySliceAccentLocale, TemporalSpokenSafetySliceSpeedPitch, TemporalSpokenSafetySliceCodecTransform, TemporalSpokenSafetySliceClipping, TemporalSpokenSafetySliceDerivativeCompilation, TemporalSpokenSafetySlicePhoneticConfusable, TemporalSpokenSafetySlicePartialToken}
}

func temporalSpokenSafetyRequiredCleanSlices() []string {
	return []string{TemporalSpokenSafetySliceWordless, TemporalSpokenSafetySliceMusicOnly, TemporalSpokenSafetySliceNearMatch, TemporalSpokenSafetySliceTargetLocale}
}

func temporalSpokenSafetyCoversSlices(observed map[string]struct{}, required []string) bool {
	if len(observed) != len(required) {
		return false
	}
	for _, slice := range required {
		if _, exists := observed[slice]; !exists {
			return false
		}
	}
	return true
}

func validateTemporalSpokenSafetyCertificationReport(report TemporalSpokenSafetyCertificationReport) error {
	if report.SchemaVersion != TemporalSpokenSafetyCertificationSchemaVersion || report.ContractVersion != TemporalSpokenSafetyCertificationContractVersion || report.ScoredAt.IsZero() || !reviewSHA256(report.AuthoritySHA256) || !reviewSHA256(report.SpokenSafetyReportSHA256) || !reviewSHA256(report.PolicySHA256) || report.PositiveSources < temporalSpokenSafetyMinimumPositiveFamilies || report.PositiveFamilies != report.PositiveSources || report.PositiveIntervals <= 0 || report.CleanSources <= 0 || report.CertificationStatus != TemporalSpokenSafetyCertificationPassed && report.CertificationStatus != TemporalSpokenSafetyCertificationFailed || report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed || report.NextAction != TemporalSpokenSafetyCertificationNextAction {
		return fmt.Errorf("spoken-safety certification identity, counts, permissions, or status is invalid")
	}
	if report.DetectedPositiveSources+report.MissedPositiveSources != report.PositiveSources || report.DetectedPositiveIntervals > report.PositiveIntervals || report.SourceRecall != float64(report.DetectedPositiveSources)/float64(report.PositiveSources) || report.CleanFalsePositiveSources > report.CleanSources || len(report.Cases) != report.PositiveSources+report.CleanSources || len(report.CleanSlices) == 0 {
		return fmt.Errorf("spoken-safety certification summary is inconsistent")
	}
	wantLower := 0.0
	if report.MissedPositiveSources == 0 {
		wantLower = math.Pow(0.05, 1/float64(report.PositiveFamilies))
	}
	if report.SourceRecallExactLower95 != wantLower {
		return fmt.Errorf("spoken-safety certification exact lower bound does not reproduce")
	}
	previousAlias := ""
	positive, detected, missed, clean, falsePositive, holds, intervals, detectedIntervals := 0, 0, 0, 0, 0, 0, 0, 0
	for _, item := range report.Cases {
		if !validTemporalSpokenSafetyChallengeAlias(item.Alias) || item.Alias <= previousAlias {
			return fmt.Errorf("spoken-safety certification cases are invalid or disordered")
		}
		previousAlias = item.Alias
		switch item.Label {
		case TemporalSpokenSafetyChallengePositive:
			positive++
			intervals += item.PositiveIntervals
			detectedIntervals += item.DetectedPositiveIntervals
			switch item.Outcome {
			case TemporalSpokenSafetyOutcomeDetected:
				detected++
			case TemporalSpokenSafetyOutcomeMissed:
				missed++
			case TemporalSpokenSafetyOutcomeCoverageHold:
				missed++
				holds++
			default:
				return fmt.Errorf("spoken-safety positive case has invalid outcome")
			}
		case TemporalSpokenSafetyChallengeClean:
			clean++
			if item.PositiveIntervals != 0 || item.DetectedPositiveIntervals != 0 {
				return fmt.Errorf("spoken-safety clean case contains interval counts")
			}
			switch item.Outcome {
			case TemporalSpokenSafetyOutcomeFalsePositive:
				falsePositive++
			case TemporalSpokenSafetyOutcomeCoverageHold:
				holds++
			case TemporalSpokenSafetyOutcomeClean:
			default:
				return fmt.Errorf("spoken-safety clean case has invalid outcome")
			}
		default:
			return fmt.Errorf("spoken-safety certification case has unknown label")
		}
	}
	if positive != report.PositiveSources || detected != report.DetectedPositiveSources || missed != report.MissedPositiveSources || clean != report.CleanSources || falsePositive != report.CleanFalsePositiveSources || holds != report.CoverageHolds || intervals != report.PositiveIntervals || detectedIntervals != report.DetectedPositiveIntervals {
		return fmt.Errorf("spoken-safety certification cases do not reproduce summary")
	}
	previousSlice := ""
	allCleanPassed := true
	for _, metric := range report.CleanSlices {
		if strings.TrimSpace(metric.Slice) == "" || metric.Slice <= previousSlice || metric.CleanSources <= 0 || metric.FalsePositives < 0 || metric.FalsePositives > metric.CleanSources || metric.FalsePositiveRate != float64(metric.FalsePositives)/float64(metric.CleanSources) || metric.Passed != (metric.FalsePositiveRate <= temporalSpokenSafetyMaximumCleanFPRate) {
			return fmt.Errorf("spoken-safety clean slice is invalid or disordered")
		}
		previousSlice = metric.Slice
		allCleanPassed = allCleanPassed && metric.Passed
	}
	wantPassed := report.MissedPositiveSources == 0 && report.SourceRecallExactLower95 >= 0.95 && report.CoverageHolds == 0 && allCleanPassed
	if (report.CertificationStatus == TemporalSpokenSafetyCertificationPassed) != wantPassed {
		return fmt.Errorf("spoken-safety certification status does not reproduce")
	}
	return nil
}
