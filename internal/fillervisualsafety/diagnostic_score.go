package fillervisualsafety

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"sort"
	"time"
)

type diagnosticFrameScore struct {
	atMS  int64
	score float64
}

type diagnosticCleanBucket struct {
	families      int
	falsePositive int
}

// EvaluatePortableDiagnostic is the diagnostic module's external interface.
// It verifies locked truth separately from exact candidate output and returns
// a non-authorizing threshold sweep plus a small targeted-review worklist.
func EvaluatePortableDiagnostic(authority PortableDiagnosticAuthority, capability PortableCapability, profile CoverageProfile, runs []PortableDiagnosticRun, scoredAt time.Time) (PortableDiagnosticReport, error) {
	if ValidatePortableDiagnosticAuthority(authority, capability, profile) != nil ||
		validatePortableDiagnosticRuns(authority, capability, profile, runs) != nil || scoredAt.IsZero() ||
		scoredAt.Location() != time.UTC || scoredAt.Before(authority.AuthoredAt) {
		return PortableDiagnosticReport{}, errors.New("portable visual diagnostic input is invalid")
	}
	report := scorePortableDiagnostic(authority, capability, runs, scoredAt)
	if err := ValidatePortableDiagnosticReport(authority, capability, profile, runs, report); err != nil {
		return PortableDiagnosticReport{}, err
	}
	return report, nil
}

func ValidatePortableDiagnosticReport(authority PortableDiagnosticAuthority, capability PortableCapability, profile CoverageProfile, runs []PortableDiagnosticRun, report PortableDiagnosticReport) error {
	if ValidatePortableDiagnosticAuthority(authority, capability, profile) != nil ||
		validatePortableDiagnosticRuns(authority, capability, profile, runs) != nil || report.ScoredAt.IsZero() ||
		report.ScoredAt.Location() != time.UTC || report.ScoredAt.Before(authority.AuthoredAt) || report.SHA256 == "" ||
		report.SHA256 != PortableDiagnosticReportSHA256(report) {
		return errors.New("portable visual diagnostic report identity is invalid")
	}
	want := scorePortableDiagnostic(authority, capability, runs, report.ScoredAt)
	if !reflect.DeepEqual(want, report) {
		return errors.New("portable visual diagnostic report does not reproduce")
	}
	return nil
}

func PortableDiagnosticReportSHA256(report PortableDiagnosticReport) string {
	report.SHA256 = ""
	return digestJSON(report)
}

func scorePortableDiagnostic(authority PortableDiagnosticAuthority, capability PortableCapability, runs []PortableDiagnosticRun, scoredAt time.Time) PortableDiagnosticReport {
	report := PortableDiagnosticReport{
		SchemaVersion: PortableDiagnosticReportSchemaVersion, ContractVersion: PortableDiagnosticReportContractVersion,
		ScoredAt: scoredAt, AuthoritySHA256: authority.SHA256, CapabilitySHA256: capability.SHA256,
		CoverageProfileSHA256: authority.CoverageProfileSHA256, ModelID: authority.ModelID,
		PositiveOutputLabel: authority.PositiveOutputLabel, ScoreTransform: authority.ScoreTransform,
		Cases: []PortableDiagnosticCaseReport{}, Thresholds: []PortableDiagnosticThreshold{},
		TargetedReviewAliases: []string{}, BlindHumanAuditRequired: false, TruthCreatedByCandidate: false,
		TrainingAllowed: false, ProductionAdmissionAllowed: false,
		NextAction: "expand_source_family_disjoint_challenge",
	}
	model, _ := diagnosticModel(capability, authority.ModelID)
	positiveIndex := slices.Index(model.OutputLabels, authority.PositiveOutputLabel)
	for index, item := range authority.Cases {
		run := runs[index]
		result := PortableDiagnosticCaseReport{
			Alias: item.Alias, TruthLabel: item.TruthLabel, RunState: run.State, FailureCode: run.FailureCode,
			Thresholds: []PortableDiagnosticCaseThreshold{}, TargetedReasons: []string{},
		}
		var scores []diagnosticFrameScore
		if run.State == DiagnosticRunComplete {
			scores = diagnosticScores(model.ID, positiveIndex, authority.ScoreTransform, run)
			result.ScoreAvailable = true
			result.FrameCount = len(scores)
			result.MaximumScore, result.MaximumScoreAtMS = maximumDiagnosticScore(scores)
		} else {
			result.TargetedReasons = append(result.TargetedReasons, "operational_failure")
		}
		for _, threshold := range authority.Thresholds {
			metric := PortableDiagnosticCaseThreshold{Threshold: threshold}
			if run.State == DiagnosticRunComplete {
				metric.Signaled = diagnosticSignaled(scores, threshold)
				if item.TruthLabel == DiagnosticTruthPositive {
					metric.DetectedPositiveIntervals = detectedDiagnosticIntervals(item.PositiveIntervals, scores, threshold)
				}
			}
			result.Thresholds = append(result.Thresholds, metric)
		}
		lowest, highest := result.Thresholds[0], result.Thresholds[len(result.Thresholds)-1]
		switch item.TruthLabel {
		case DiagnosticTruthPositive:
			if run.State == DiagnosticRunComplete && lowest.DetectedPositiveIntervals != len(item.PositiveIntervals) {
				result.TargetedReasons = append(result.TargetedReasons, "positive_miss_at_lowest_threshold")
			}
		case DiagnosticTruthClean:
			if run.State == DiagnosticRunComplete && highest.Signaled {
				result.TargetedReasons = append(result.TargetedReasons, "clean_signal_at_highest_threshold")
			}
		case DiagnosticTruthUnresolved:
			if run.State == DiagnosticRunComplete && lowest.Signaled {
				result.TargetedReasons = append(result.TargetedReasons, "unresolved_signal")
			}
		}
		if len(result.TargetedReasons) > 0 {
			report.TargetedReviewAliases = append(report.TargetedReviewAliases, item.Alias)
		}
		report.Cases = append(report.Cases, result)
	}
	for thresholdIndex, threshold := range authority.Thresholds {
		report.Thresholds = append(report.Thresholds, scoreDiagnosticThreshold(authority, report.Cases, thresholdIndex, threshold))
	}
	if len(report.TargetedReviewAliases) > 0 {
		report.NextAction = "inspect_targeted_diagnostics"
	}
	report.SHA256 = PortableDiagnosticReportSHA256(report)
	return report
}

func diagnosticScores(modelID string, positiveIndex int, transform string, run PortableDiagnosticRun) []diagnosticFrameScore {
	scores := make([]diagnosticFrameScore, 0, len(run.Inference.Responses))
	for index, response := range run.Inference.Responses {
		modelIndex := slices.IndexFunc(response.Models, func(candidate PortableModelScores) bool { return candidate.ModelID == modelID })
		scores = append(scores, diagnosticFrameScore{
			atMS:  run.Coverage.Frames[index].ObservedMS,
			score: diagnosticTransformedScore(response.Models[modelIndex].Logits, positiveIndex, transform),
		})
	}
	return scores
}

func diagnosticTransformedScore(logits []float64, selected int, transform string) float64 {
	maximum := slices.Max(logits)
	denominator := 0.0
	for _, logit := range logits {
		denominator += math.Exp(logit - maximum)
	}
	if transform == DiagnosticScoreSoftmax {
		return math.Exp(logits[selected]-maximum) / denominator
	}
	numerator := 0.0
	for _, logit := range logits[selected:] {
		numerator += math.Exp(logit - maximum)
	}
	return numerator / denominator
}

func maximumDiagnosticScore(scores []diagnosticFrameScore) (float64, int64) {
	maximum, atMS := scores[0].score, scores[0].atMS
	for _, score := range scores[1:] {
		if score.score > maximum {
			maximum, atMS = score.score, score.atMS
		}
	}
	return maximum, atMS
}

func diagnosticSignaled(scores []diagnosticFrameScore, threshold float64) bool {
	return slices.ContainsFunc(scores, func(score diagnosticFrameScore) bool { return score.score >= threshold })
}

func detectedDiagnosticIntervals(intervals []Interval, scores []diagnosticFrameScore, threshold float64) int {
	detected := 0
	for _, interval := range intervals {
		if slices.ContainsFunc(scores, func(score diagnosticFrameScore) bool {
			return score.atMS >= interval.StartMS && score.atMS < interval.EndMS && score.score >= threshold
		}) {
			detected++
		}
	}
	return detected
}

func scoreDiagnosticThreshold(authority PortableDiagnosticAuthority, cases []PortableDiagnosticCaseReport, thresholdIndex int, threshold float64) PortableDiagnosticThreshold {
	metric := PortableDiagnosticThreshold{Threshold: threshold, CleanSlices: []PortableDiagnosticSliceMetric{}}
	buckets := map[string]*diagnosticCleanBucket{}
	for index, result := range cases {
		item := authority.Cases[index]
		caseMetric := result.Thresholds[thresholdIndex]
		if result.RunState != DiagnosticRunComplete {
			metric.OperationalHolds++
		}
		switch item.TruthLabel {
		case DiagnosticTruthPositive:
			metric.PositiveFamilies++
			if result.RunState == DiagnosticRunComplete && caseMetric.DetectedPositiveIntervals == len(item.PositiveIntervals) {
				metric.DetectedPositiveFamilies++
			} else {
				metric.MissedPositiveFamilies++
			}
		case DiagnosticTruthClean:
			metric.CleanFamilies++
			falsePositive := result.RunState == DiagnosticRunComplete && caseMetric.Signaled
			if falsePositive {
				metric.CleanFalsePositiveFamilies++
			}
			for _, slice := range item.Slices {
				bucket := buckets[slice]
				if bucket == nil {
					bucket = &diagnosticCleanBucket{}
					buckets[slice] = bucket
				}
				bucket.families++
				if falsePositive {
					bucket.falsePositive++
				}
			}
		case DiagnosticTruthUnresolved:
			metric.UnresolvedFamilies++
			if result.RunState == DiagnosticRunComplete && caseMetric.Signaled {
				metric.UnresolvedSignaledFamilies++
			}
		}
	}
	if metric.PositiveFamilies > 0 {
		metric.PositiveRecall = float64(metric.DetectedPositiveFamilies) / float64(metric.PositiveFamilies)
		metric.PositiveRecallExactLower95 = diagnosticExactLower95(metric.DetectedPositiveFamilies, metric.PositiveFamilies)
	}
	if metric.CleanFamilies > 0 {
		metric.CleanFalsePositiveRate = float64(metric.CleanFalsePositiveFamilies) / float64(metric.CleanFamilies)
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := buckets[key]
		rate := float64(bucket.falsePositive) / float64(bucket.families)
		metric.CleanSlices = append(metric.CleanSlices, PortableDiagnosticSliceMetric{
			Slice: key, CleanFamilies: bucket.families, FalsePositives: bucket.falsePositive,
			FalsePositiveRate: rate, WithinCeiling: rate <= authority.MaximumCleanFalsePositiveRate,
		})
	}
	return metric
}

// diagnosticExactLower95 returns the one-sided 95% Clopper-Pearson lower bound.
func diagnosticExactLower95(successes, trials int) float64 {
	if trials <= 0 || successes <= 0 {
		return 0
	}
	if successes > trials {
		return math.NaN()
	}
	low, high := 0.0, float64(successes)/float64(trials)
	for range 100 {
		mid := (low + high) / 2
		if diagnosticBinomialUpperTail(successes, trials, mid) < 0.05 {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

func diagnosticBinomialUpperTail(successes, trials int, probability float64) float64 {
	if probability <= 0 {
		return 0
	}
	if probability >= 1 {
		return 1
	}
	trialsGamma, _ := math.Lgamma(float64(trials + 1))
	successGamma, _ := math.Lgamma(float64(successes + 1))
	failureGamma, _ := math.Lgamma(float64(trials - successes + 1))
	logTerm := trialsGamma - successGamma - failureGamma + float64(successes)*math.Log(probability) +
		float64(trials-successes)*math.Log1p(-probability)
	term, tail := math.Exp(logTerm), 0.0
	for index := successes; index <= trials; index++ {
		tail += term
		if index < trials {
			term *= float64(trials-index) / float64(index+1) * probability / (1 - probability)
		}
	}
	return min(1, tail)
}
