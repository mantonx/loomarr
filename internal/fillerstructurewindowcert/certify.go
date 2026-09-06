package fillerstructurewindowcert

import (
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

const (
	StatusPassed = "passed"
	StatusFailed = "failed"
)

type caseScore struct {
	decided  bool
	held     bool
	wrong    bool
	failures []string
}

type sliceAccumulator struct {
	cases, decided, held, wrong int
	failures                    []string
}

// Certify replays two complete assessor-family stitches per case, scores each family against
// private truth, and then scores the shared production reducer. Evaluation failures produce a
// failed immutable report; malformed or unreplayable evidence returns an error.
func Certify(suite Suite, results []CaseResult, certifiedAt time.Time) (Report, error) {
	if err := ValidateSuite(suite); err != nil {
		return Report{}, err
	}
	if certifiedAt.IsZero() || certifiedAt != certifiedAt.UTC() {
		return Report{}, errors.New("window certification time must be canonical UTC")
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion, ContractVersion: ReportContractVersion,
		CertifiedAt: certifiedAt, SuiteSHA256: suite.SHA256,
		ReducerVersion: fillerstructure.ReducerContractVersion, BoundaryToleranceMS: BoundaryToleranceMS,
		HighByteMinimumBytes: suite.HighByteMinimumBytes,
		Cases:                len(suite.Cases), TrainingAllowed: false, AutomaticMaterializationAllowed: false,
	}
	resultByID := make(map[string]CaseResult, len(results))
	for _, result := range results {
		if !canonicalID(result.CaseID) {
			return Report{}, errors.New("window certification result case ID is invalid")
		}
		if _, duplicate := resultByID[result.CaseID]; duplicate {
			report.FailureCodes = append(report.FailureCodes, "duplicate_case_result")
		}
		resultByID[result.CaseID] = result
	}
	metrics := make(map[Slice]*sliceAccumulator, len(requiredSlices))
	for _, slice := range requiredSlices {
		metrics[slice] = &sliceAccumulator{}
	}
	var lockedProfiles []fillerstructure.AssessorProfile
	for _, item := range suite.Cases {
		for _, slice := range item.Slices {
			metrics[slice].cases++
		}
		result, ok := resultByID[item.ID]
		if !ok {
			score := caseScore{held: true, failures: []string{"missing_case_result"}}
			report.FailureCodes = append(report.FailureCodes, score.failures...)
			report.HeldCases++
			applyScore(item.Slices, metrics, score)
			continue
		}
		score, profiles, err := scoreCase(item, result)
		if err != nil {
			return Report{}, err
		}
		if lockedProfiles == nil && len(profiles) == 2 {
			lockedProfiles = profiles
		} else if len(profiles) == 2 && !reflect.DeepEqual(lockedProfiles, profiles) {
			score.wrong = true
			score.failures = append(score.failures, "assessor_profile_drift")
		}
		if score.decided {
			report.DecidedCases++
		}
		if score.held {
			report.HeldCases++
		}
		if score.wrong {
			report.WrongCases++
		}
		report.FailureCodes = append(report.FailureCodes, score.failures...)
		applyScore(item.Slices, metrics, score)
		delete(resultByID, item.ID)
	}
	if len(resultByID) != 0 {
		report.FailureCodes = append(report.FailureCodes, "unexpected_case_result")
	}
	if len(lockedProfiles) != 2 || sameFamily(lockedProfiles[0], lockedProfiles[1]) {
		report.FailureCodes = append(report.FailureCodes, "independent_family_set")
	} else {
		report.AssessorProfiles = lockedProfiles
	}
	for _, slice := range requiredSlices {
		metric := metrics[slice]
		result := SliceResult{
			Slice: slice, Cases: metric.cases, DecidedCases: metric.decided,
			HeldCases: metric.held, WrongCases: metric.wrong, FailureCodes: unique(metric.failures),
		}
		if result.Cases < MinimumSliceCases {
			result.FailureCodes = append(result.FailureCodes, "insufficient_slice_cases")
		}
		if result.DecidedCases < result.Cases {
			result.FailureCodes = append(result.FailureCodes, "incomplete_slice_decisions")
		}
		result.FailureCodes = unique(result.FailureCodes)
		result.Passed = len(result.FailureCodes) == 0 && result.HeldCases == 0 && result.WrongCases == 0
		if !result.Passed {
			report.FailureCodes = append(report.FailureCodes, "slice_failed:"+string(slice))
		}
		report.Slices = append(report.Slices, result)
	}
	report.FailureCodes = unique(report.FailureCodes)
	if len(report.FailureCodes) == 0 && report.DecidedCases == report.Cases && report.HeldCases == 0 && report.WrongCases == 0 {
		report.Status = StatusPassed
		report.NextAction = "run_locked_short_long_shadow_comparison"
	} else {
		report.Status = StatusFailed
		report.NextAction = "diagnose_long_reel_certification_failures"
	}
	report.SHA256 = ReportSHA256(report)
	return report, nil
}

func scoreCase(item Case, result CaseResult) (caseScore, []fillerstructure.AssessorProfile, error) {
	if len(result.Stitches) != 2 {
		return caseScore{held: true, failures: []string{"stitch_count"}}, nil, nil
	}
	stitches := slices.Clone(result.Stitches)
	sort.Slice(stitches, func(i, j int) bool { return stitches[i].Assessor.ID < stitches[j].Assessor.ID })
	profiles := make([]fillerstructure.AssessorProfile, 2)
	candidates := make([]fillerstructure.Candidate, 0, 2)
	var input fillerstructure.AssessmentInput
	score := caseScore{decided: true}
	for index, stitch := range stitches {
		if err := fillerstructurewindow.ValidateStitchResult(stitch); err != nil {
			return caseScore{}, nil, errors.New("window certification stitch does not replay")
		}
		if stitch.MediaSet.SHA256 != item.MediaSet.SHA256 || stitch.BoundaryToleranceMS != BoundaryToleranceMS {
			return caseScore{}, nil, errors.New("window certification stitch authority drifted")
		}
		profiles[index] = stitch.Assessor
		candidateInput, candidate, err := fillerstructurewindow.ReducerCandidate(stitch)
		if err != nil {
			return caseScore{}, nil, err
		}
		if index == 0 {
			input = candidateInput
		} else if candidateInput.SHA256 != input.SHA256 {
			return caseScore{}, nil, errors.New("window certification candidates do not share input")
		}
		candidates = append(candidates, candidate)
		if stitch.Status != fillerstructurewindow.StitchComplete {
			score.held = true
			score.decided = false
			score.failures = append(score.failures, "held_family_stitch")
			continue
		}
		score.failures = append(score.failures, timelineFailures(stitch.Segments, item.Truth, "family")...)
	}
	if len(score.failures) != 0 {
		score.wrong = containsSemanticFailure(score.failures)
	}
	decision := fillerstructure.Reduce(fillerstructure.Request{
		Source: item.MediaSet.Plan.Source, Input: input,
		BoundaryToleranceMS: BoundaryToleranceMS, Candidates: candidates,
	})
	if decision.Status != fillerstructure.StatusConfirmed {
		score.held = true
		score.decided = false
		score.failures = append(score.failures, "reducer_hold")
	} else {
		segments := make([]fillerstructure.Segment, len(decision.Segments))
		for index, segment := range decision.Segments {
			segments[index] = fillerstructure.Segment{StartMS: segment.StartMS, EndMS: segment.EndMS, Role: segment.Role}
		}
		failures := timelineFailures(segments, item.Truth, "reducer")
		score.failures = append(score.failures, failures...)
		score.wrong = score.wrong || len(failures) != 0
	}
	score.failures = unique(score.failures)
	return score, profiles, nil
}

func timelineFailures(actual, truth []fillerstructure.Segment, prefix string) []string {
	if len(actual) < len(truth) {
		return []string{prefix + "_under_split"}
	}
	if len(actual) > len(truth) {
		return []string{prefix + "_over_split"}
	}
	var failures []string
	for index := range truth {
		if actual[index].Role != truth[index].Role {
			failures = append(failures, prefix+"_role_error")
		}
		if index+1 < len(truth) && absolute(actual[index].EndMS-truth[index].EndMS) > BoundaryToleranceMS {
			failures = append(failures, prefix+"_boundary_error")
		}
	}
	return unique(failures)
}

func applyScore(slices []Slice, metrics map[Slice]*sliceAccumulator, score caseScore) {
	for _, slice := range slices {
		metric := metrics[slice]
		if score.decided {
			metric.decided++
		}
		if score.held {
			metric.held++
		}
		if score.wrong {
			metric.wrong++
		}
		metric.failures = append(metric.failures, score.failures...)
	}
}

func sameFamily(left, right fillerstructure.AssessorProfile) bool {
	return strings.EqualFold(strings.TrimSpace(left.ModelFamily), strings.TrimSpace(right.ModelFamily))
}

func containsSemanticFailure(failures []string) bool {
	for _, failure := range failures {
		if strings.Contains(failure, "under_split") || strings.Contains(failure, "over_split") ||
			strings.Contains(failure, "role_error") || strings.Contains(failure, "boundary_error") {
			return true
		}
	}
	return false
}

func unique(values []string) []string {
	sort.Strings(values)
	return slices.Compact(values)
}

func absolute(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
