package fillerreview

import (
	"math"
	"sort"
	"time"
)

type temporalSpokenSafetyCleanBucket struct {
	sources, falsePositives int
}

func scoreTemporalSpokenSafetyCertification(loaded temporalSpokenSafetyCertificationLoaded, scoredAt time.Time) TemporalSpokenSafetyCertificationReport {
	report := TemporalSpokenSafetyCertificationReport{
		SchemaVersion: TemporalSpokenSafetyCertificationSchemaVersion, ContractVersion: TemporalSpokenSafetyCertificationContractVersion,
		ScoredAt: scoredAt, AuthoritySHA256: loaded.authoritySHA, SpokenSafetyReportSHA256: loaded.projectionSHA,
		PolicySHA256: loaded.projection.PolicySHA256, ChallengeKind: loaded.authority.ChallengeKind,
		CertificationStatus: TemporalSpokenSafetyCertificationPassed,
		NextAction:          TemporalSpokenSafetyCertificationNextAction,
	}
	if report.ChallengeKind == TemporalSpokenSafetyChallengeDevelopment {
		report.CertificationStatus = TemporalSpokenSafetyDiagnosticPassed
	}
	cleanBuckets := map[string]*temporalSpokenSafetyCleanBucket{}
	for _, challenge := range loaded.authority.Cases {
		source := loaded.sources[challenge.SourceSHA256]
		result := TemporalSpokenSafetyCertificationCase{Alias: challenge.Alias, Label: challenge.Label}
		switch challenge.Label {
		case TemporalSpokenSafetyChallengePositive:
			report.PositiveSources++
			report.PositiveFamilies++
			result.PositiveIntervals = len(challenge.PositiveIntervals)
			for _, expected := range challenge.PositiveIntervals {
				report.PositiveIntervals++
				if temporalSpokenSafetyPositiveIntervalDetected(expected, source.Matches) {
					report.DetectedPositiveIntervals++
					result.DetectedPositiveIntervals++
				}
			}
			if result.DetectedPositiveIntervals == result.PositiveIntervals && source.Disposition == TemporalSpokenSafetyDispositionProhibited {
				report.DetectedPositiveSources++
				result.Outcome = TemporalSpokenSafetyOutcomeDetected
			} else if source.Disposition == TemporalSpokenSafetyDispositionCoverage {
				report.CoverageHolds++
				report.MissedPositiveSources++
				result.Outcome = TemporalSpokenSafetyOutcomeCoverageHold
			} else {
				report.MissedPositiveSources++
				result.Outcome = TemporalSpokenSafetyOutcomeMissed
			}
		case TemporalSpokenSafetyChallengeClean:
			report.CleanSources++
			falsePositive := len(source.Matches) != 0
			if source.Disposition == TemporalSpokenSafetyDispositionCoverage && !falsePositive {
				report.CoverageHolds++
				result.Outcome = TemporalSpokenSafetyOutcomeCoverageHold
			} else if falsePositive {
				report.CleanFalsePositiveSources++
				result.Outcome = TemporalSpokenSafetyOutcomeFalsePositive
			} else {
				result.Outcome = TemporalSpokenSafetyOutcomeClean
			}
			for _, slice := range append([]string{"locale:" + challenge.Locale}, challenge.Slices...) {
				bucket := cleanBuckets[slice]
				if bucket == nil {
					bucket = &temporalSpokenSafetyCleanBucket{}
					cleanBuckets[slice] = bucket
				}
				bucket.sources++
				if falsePositive {
					bucket.falsePositives++
				}
			}
		}
		report.Cases = append(report.Cases, result)
	}
	sort.Slice(report.Cases, func(i, j int) bool { return report.Cases[i].Alias < report.Cases[j].Alias })
	report.SourceRecall = float64(report.DetectedPositiveSources) / float64(report.PositiveSources)
	report.SourceRecallExactLower95 = temporalSpokenSafetyExactLower95(report.DetectedPositiveSources, report.PositiveFamilies)
	keys := make([]string, 0, len(cleanBuckets))
	for key := range cleanBuckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := cleanBuckets[key]
		rate := float64(bucket.falsePositives) / float64(bucket.sources)
		report.CleanSlices = append(report.CleanSlices, TemporalSpokenSafetyCleanSliceMetric{Slice: key, CleanSources: bucket.sources, FalsePositives: bucket.falsePositives, FalsePositiveRate: rate, Passed: rate <= temporalSpokenSafetyMaximumCleanFPRate})
	}
	if report.PositiveFamilies < temporalSpokenSafetyMinimumPositiveFamilies || report.MissedPositiveSources != 0 || report.SourceRecallExactLower95 < 0.95 || report.CoverageHolds != 0 {
		report.CertificationStatus = TemporalSpokenSafetyCertificationFailed
	}
	for _, metric := range report.CleanSlices {
		if !metric.Passed {
			report.CertificationStatus = TemporalSpokenSafetyCertificationFailed
		}
	}
	return report
}

// temporalSpokenSafetyExactLower95 returns the one-sided 95% Clopper-Pearson
// lower confidence bound. Certification still requires zero misses; retaining
// the actual bound for failed runs makes model improvements measurable without
// weakening that gate.
func temporalSpokenSafetyExactLower95(successes, trials int) float64 {
	if trials <= 0 || successes <= 0 {
		return 0
	}
	if successes > trials {
		return math.NaN()
	}
	low, high := 0.0, float64(successes)/float64(trials)
	for range 100 {
		mid := (low + high) / 2
		if temporalSpokenSafetyBinomialUpperTail(successes, trials, mid) < 0.05 {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

func temporalSpokenSafetyBinomialUpperTail(successes, trials int, probability float64) float64 {
	if probability <= 0 {
		return 0
	}
	if probability >= 1 {
		return 1
	}
	trialsGamma, _ := math.Lgamma(float64(trials + 1))
	successGamma, _ := math.Lgamma(float64(successes + 1))
	failureGamma, _ := math.Lgamma(float64(trials - successes + 1))
	logTerm := trialsGamma - successGamma - failureGamma + float64(successes)*math.Log(probability) + float64(trials-successes)*math.Log1p(-probability)
	term := math.Exp(logTerm)
	tail := term
	for i := successes; i < trials; i++ {
		term *= float64(trials-i) / float64(i+1) * probability / (1 - probability)
		tail += term
	}
	return min(1, tail)
}

func temporalSpokenSafetyPositiveIntervalDetected(expected TemporalSpokenSafetyPositiveInterval, matches []TemporalSpokenSafetyMatch) bool {
	for _, match := range matches {
		if match.RuleID == expected.RuleID && match.Class == TemporalSpokenSafetyMatchProhibited && match.StartMS < expected.EndMS && match.EndMS > expected.StartMS {
			return true
		}
	}
	return false
}
