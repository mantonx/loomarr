package fillersafetycert

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

type cleanBucket struct{ sources, falsePositives int }

func score(loaded loadedCertification, scoredAt time.Time) Report {
	report := Report{
		SchemaVersion: SchemaVersion, ContractVersion: ContractVersion, ScoredAt: scoredAt.UTC(),
		AuthoritySHA256: loaded.authoritySHA, ResultManifestSHA256: loaded.manifestSHA,
		PolicySHA256: loaded.authority.PolicySHA256, ProposerSHA256: loaded.authority.ProposerSHA256,
		Implementation: loaded.authority.Implementation, ChallengeKind: loaded.authority.ChallengeKind,
		CertificationStatus: StatusPassed, NextAction: NextAction,
	}
	if report.ChallengeKind == ChallengeDevelopment {
		report.CertificationStatus = StatusDiagnosticPassed
	}
	cleanBuckets := map[string]*cleanBucket{}
	for _, challenge := range loaded.authority.Cases {
		terminal := loaded.runs[challenge.Alias].Events[len(loaded.runs[challenge.Alias].Events)-1].Terminal
		result := CaseReport{Alias: challenge.Alias, Label: challenge.Label}
		hold := coverageHold(terminal.Evidence)
		switch challenge.Label {
		case LabelPositive:
			report.PositiveSources++
			report.PositiveFamilies++
			result.PositiveIntervals = len(challenge.PositiveIntervals)
			for _, expected := range challenge.PositiveIntervals {
				report.PositiveIntervals++
				if positiveIntervalDetected(expected, terminal.Evidence) {
					report.DetectedPositiveIntervals++
					result.DetectedPositiveIntervals++
				}
			}
			if hold {
				report.CoverageHolds++
				report.MissedPositiveSources++
				result.Outcome = OutcomeCoverageHold
			} else if result.DetectedPositiveIntervals == result.PositiveIntervals {
				report.DetectedPositiveSources++
				result.Outcome = OutcomeDetected
			} else {
				report.MissedPositiveSources++
				result.Outcome = OutcomeMissed
			}
		case LabelClean:
			report.CleanSources++
			falsePositive := cleanAudioFalsePositive(terminal.Evidence)
			if hold {
				report.CoverageHolds++
				result.Outcome = OutcomeCoverageHold
			} else if falsePositive {
				report.CleanFalsePositiveSources++
				result.Outcome = OutcomeFalsePositive
			} else {
				result.Outcome = OutcomeClean
			}
			for _, slice := range append([]string{"locale:" + challenge.Locale}, challenge.Slices...) {
				bucket := cleanBuckets[slice]
				if bucket == nil {
					bucket = &cleanBucket{}
					cleanBuckets[slice] = bucket
				}
				bucket.sources++
				if falsePositive && !hold {
					bucket.falsePositives++
				}
			}
		}
		report.Cases = append(report.Cases, result)
	}
	sort.Slice(report.Cases, func(i, j int) bool { return report.Cases[i].Alias < report.Cases[j].Alias })
	report.SourceRecall = float64(report.DetectedPositiveSources) / float64(report.PositiveFamilies)
	report.SourceRecallExactLower95 = exactLower95(report.DetectedPositiveSources, report.PositiveFamilies)
	keys := make([]string, 0, len(cleanBuckets))
	for key := range cleanBuckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := cleanBuckets[key]
		rate := float64(bucket.falsePositives) / float64(bucket.sources)
		report.CleanSlices = append(report.CleanSlices, CleanSliceMetric{
			Slice: key, CleanSources: bucket.sources, FalsePositives: bucket.falsePositives,
			FalsePositiveRate: rate, Passed: rate <= MaximumCleanFPRate,
		})
	}
	if report.PositiveFamilies < MinimumPositiveFamilies || report.MissedPositiveSources != 0 ||
		report.SourceRecallExactLower95 < 0.95 || report.CoverageHolds != 0 {
		report.CertificationStatus = StatusFailed
	}
	for _, metric := range report.CleanSlices {
		if !metric.Passed {
			report.CertificationStatus = StatusFailed
		}
	}
	return report
}

func positiveIntervalDetected(expected PositiveInterval, evidence fillersafety.Evidence) bool {
	for index, candidate := range evidence.Candidates {
		assessment := evidence.Audio[index]
		if candidate.StartMS < expected.EndMS && candidate.EndMS > expected.StartMS &&
			assessment.State == fillersafety.AudioDetected && slices.Contains(assessment.MatchedRuleIDs, expected.RuleID) {
			return true
		}
	}
	return false
}

func cleanAudioFalsePositive(evidence fillersafety.Evidence) bool {
	return slices.ContainsFunc(evidence.Audio, func(value fillersafety.AudioAssessment) bool {
		return value.State == fillersafety.AudioDetected
	})
}

func coverageHold(evidence fillersafety.Evidence) bool {
	if evidence.ProposalState != fillersafety.ProposalComplete {
		return true
	}
	if slices.ContainsFunc(evidence.Audio, func(value fillersafety.AudioAssessment) bool {
		switch value.State {
		case fillersafety.AudioUnclear, fillersafety.AudioFailed, fillersafety.AudioInvalidResponse, fillersafety.AudioDetectedUnprojectable:
			return true
		default:
			return false
		}
	}) {
		return true
	}
	switch evidence.Video {
	case fillersafety.VideoIncomplete, fillersafety.VideoFailed, fillersafety.VideoInvalidResponse,
		fillersafety.VideoProhibited, fillersafety.VideoProhibitedUnprojectable:
		return true
	default:
		return false
	}
}

// exactLower95 returns the one-sided 95% Clopper-Pearson lower bound.
func exactLower95(successes, trials int) float64 {
	if trials <= 0 || successes <= 0 {
		return 0
	}
	if successes > trials {
		return math.NaN()
	}
	low, high := 0.0, float64(successes)/float64(trials)
	for range 100 {
		mid := (low + high) / 2
		if binomialUpperTail(successes, trials, mid) < 0.05 {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

func binomialUpperTail(successes, trials int, probability float64) float64 {
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
	term := math.Exp(logTerm)
	tail := term
	for index := successes; index < trials; index++ {
		term *= float64(trials-index) / float64(index+1) * probability / (1 - probability)
		tail += term
	}
	return min(1, tail)
}
