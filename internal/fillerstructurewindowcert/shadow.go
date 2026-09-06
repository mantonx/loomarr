package fillerstructurewindowcert

import (
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	ShadowStatusPassed = "passed"
	ShadowStatusFailed = "failed"
)

// CompareShortLong replays and compares the two production reducer representations over one
// locked corpus. Valid disagreement becomes a failed report; malformed evidence returns an error.
func CompareShortLong(manifestSHA256, certificationSHA256 string, expectedAliases []string, cases []ShadowCase, comparedAt time.Time) (ShadowReport, error) {
	report, err := compareShortLong(manifestSHA256, certificationSHA256, expectedAliases, cases, comparedAt)
	if err != nil {
		return ShadowReport{}, err
	}
	if err := ValidateShadowReport(report); err != nil {
		return ShadowReport{}, err
	}
	return report, nil
}

func compareShortLong(manifestSHA256, certificationSHA256 string, expectedAliases []string, cases []ShadowCase, comparedAt time.Time) (ShadowReport, error) {
	if !validShadowDigest(manifestSHA256) || !validShadowDigest(certificationSHA256) ||
		comparedAt.IsZero() || comparedAt != comparedAt.UTC() {
		return ShadowReport{}, errors.New("short-long shadow authority or comparison time is invalid")
	}
	expected := slices.Clone(expectedAliases)
	if len(expected) != ShadowRequiredCases || !slices.IsSorted(expected) || !validShadowAliases(expected) ||
		len(slices.Compact(slices.Clone(expected))) != len(expected) {
		return ShadowReport{}, errors.New("short-long shadow requires the canonical complete corpus")
	}
	byAlias := make(map[string]ShadowCase, len(cases))
	for _, item := range cases {
		if strings.TrimSpace(item.Alias) != item.Alias || item.Alias == "" {
			return ShadowReport{}, errors.New("short-long shadow case alias is invalid")
		}
		if _, duplicate := byAlias[item.Alias]; duplicate {
			return ShadowReport{}, errors.New("short-long shadow repeats a case")
		}
		byAlias[item.Alias] = item
	}
	report := ShadowReport{
		SchemaVersion: ShadowReportSchemaVersion, ContractVersion: ShadowReportContractVersion,
		ComparedAt: comparedAt, WindowSetManifestSHA256: manifestSHA256,
		WindowCertificationSHA256: certificationSHA256, ReducerVersion: fillerstructure.ReducerContractVersion,
		BoundaryToleranceMS: BoundaryToleranceMS, ExpectedAliases: expected,
	}
	var lockedProfiles []ShadowProfilePair
	for _, alias := range expected {
		item, ok := byAlias[alias]
		if !ok {
			report.FailedCases++
			report.FailureCodes = append(report.FailureCodes, "missing_case")
			continue
		}
		result, profiles, err := compareShadowCase(item)
		if err != nil {
			return ShadowReport{}, err
		}
		if lockedProfiles == nil {
			lockedProfiles = profiles
		} else if !reflect.DeepEqual(lockedProfiles, profiles) {
			result.FailureCodes = append(result.FailureCodes, "profile_drift")
			result.Passed = false
		}
		result.FailureCodes = unique(result.FailureCodes)
		report.Cases = append(report.Cases, result)
		if result.Passed {
			report.PassedCases++
		} else {
			report.FailedCases++
			report.FailureCodes = append(report.FailureCodes, result.FailureCodes...)
		}
		delete(byAlias, alias)
	}
	if len(byAlias) != 0 {
		return ShadowReport{}, errors.New("short-long shadow contains an unexpected case")
	}
	report.Profiles = lockedProfiles
	report.FailureCodes = unique(report.FailureCodes)
	if report.PassedCases == ShadowRequiredCases && report.FailedCases == 0 && len(report.FailureCodes) == 0 && len(report.Profiles) == 2 {
		report.Status = ShadowStatusPassed
		report.NextAction = "issue_separately_reviewed_long_reel_materialization_authority"
	} else {
		report.Status = ShadowStatusFailed
		report.NextAction = "diagnose_short_long_representation_disagreement"
	}
	report.SHA256 = ShadowReportSHA256(report)
	return report, nil
}

func validShadowAliases(aliases []string) bool {
	for _, alias := range aliases {
		if alias == "" || strings.TrimSpace(alias) != alias || len(alias) > 256 {
			return false
		}
	}
	return true
}

func compareShadowCase(item ShadowCase) (ShadowCaseResult, []ShadowProfilePair, error) {
	if err := fillerstructure.ValidateArtifact(item.CompleteVideo); err != nil {
		return ShadowCaseResult{}, nil, errors.New("short-long shadow complete-video artifact does not replay")
	}
	if err := fillerstructure.ValidateArtifact(item.WindowMediaSet); err != nil {
		return ShadowCaseResult{}, nil, errors.New("short-long shadow window artifact does not replay")
	}
	short, long := item.CompleteVideo, item.WindowMediaSet
	if short.Decision.Input.Kind != fillerstructure.AssessmentInputCompleteVideo ||
		long.Decision.Input.Kind != fillerstructure.AssessmentInputWindowMediaSet {
		return ShadowCaseResult{}, nil, errors.New("short-long shadow artifacts use the wrong representations")
	}
	if short.Decision.Source != long.Decision.Source || short.ReducerVersion != long.ReducerVersion ||
		short.ReducerVersion != fillerstructure.ReducerContractVersion ||
		short.BoundaryToleranceMS != long.BoundaryToleranceMS || short.BoundaryToleranceMS != BoundaryToleranceMS ||
		short.Decision.Input.ProfileSHA256 != long.Decision.Input.ProfileSHA256 {
		return ShadowCaseResult{}, nil, errors.New("short-long shadow common authority drifted")
	}
	profiles, err := pairShadowProfiles(short.Decision.Candidates, long.Decision.Candidates)
	if err != nil {
		return ShadowCaseResult{}, nil, err
	}
	result := ShadowCaseResult{Alias: item.Alias, CompleteVideo: short, WindowMediaSet: long}
	result.FailureCodes = compareShadowDecisions(short.Decision, long.Decision, short.BoundaryToleranceMS)
	result.Passed = len(result.FailureCodes) == 0
	return result, profiles, nil
}

func compareShadowDecisions(short, long fillerstructure.Decision, tolerance int64) []string {
	var failures []string
	if short.Status != fillerstructure.StatusConfirmed {
		failures = append(failures, "complete_video_held")
	}
	if long.Status != fillerstructure.StatusConfirmed {
		failures = append(failures, "window_media_set_held")
	}
	if short.Status != fillerstructure.StatusConfirmed || long.Status != fillerstructure.StatusConfirmed {
		return unique(failures)
	}
	if short.Unit != long.Unit {
		failures = append(failures, "unit_disagreement")
	}
	if short.Role != long.Role {
		failures = append(failures, "standalone_role_disagreement")
	}
	if len(short.Segments) != len(long.Segments) {
		return unique(append(failures, "interval_count_disagreement"))
	}
	for index := range short.Segments {
		if short.Segments[index].Role != long.Segments[index].Role {
			failures = append(failures, "interval_role_disagreement")
		}
		if short.Segments[index].Disposition != long.Segments[index].Disposition {
			failures = append(failures, "interval_disposition_disagreement")
		}
		if index+1 < len(short.Segments) && absolute(short.Segments[index].EndMS-long.Segments[index].EndMS) > tolerance {
			failures = append(failures, "boundary_disagreement")
		}
	}
	return unique(failures)
}

func pairShadowProfiles(short, long []fillerstructure.Candidate) ([]ShadowProfilePair, error) {
	if len(short) != 2 || len(long) != 2 {
		return nil, errors.New("short-long shadow requires exactly two candidates per representation")
	}
	shortByFamily := make(map[string]fillerstructure.AssessorProfile, 2)
	longByFamily := make(map[string]fillerstructure.AssessorProfile, 2)
	for _, candidate := range short {
		profile := fillerstructure.Profile(candidate.Assessor)
		if fillerstructure.ValidateAssessorProfile(profile) != nil {
			return nil, errors.New("short-long shadow complete-video profile is invalid")
		}
		if _, duplicate := shortByFamily[profile.ModelFamily]; duplicate {
			return nil, errors.New("short-long shadow repeats a complete-video family")
		}
		shortByFamily[profile.ModelFamily] = profile
	}
	for _, candidate := range long {
		profile := fillerstructure.Profile(candidate.Assessor)
		if fillerstructure.ValidateAssessorProfile(profile) != nil {
			return nil, errors.New("short-long shadow window profile is invalid")
		}
		if _, duplicate := longByFamily[profile.ModelFamily]; duplicate {
			return nil, errors.New("short-long shadow repeats a window family")
		}
		longByFamily[profile.ModelFamily] = profile
	}
	if len(shortByFamily) != 2 || len(longByFamily) != 2 {
		return nil, errors.New("short-long shadow requires two independent families")
	}
	var pairs []ShadowProfilePair
	for family, shortProfile := range shortByFamily {
		longProfile, ok := longByFamily[family]
		if !ok || !sameShadowModel(shortProfile, longProfile) {
			return nil, errors.New("short-long shadow model families do not match")
		}
		pairs = append(pairs, ShadowProfilePair{ModelFamily: family, CompleteVideo: shortProfile, WindowMediaSet: longProfile})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].ModelFamily < pairs[j].ModelFamily })
	return pairs, nil
}

func sameShadowModel(short, long fillerstructure.AssessorProfile) bool {
	return short.ModelFamily == long.ModelFamily && short.Provider == long.Provider && short.Model == long.Model &&
		short.ModelDigest == long.ModelDigest && short.CapabilitySHA256 == long.CapabilitySHA256
}
