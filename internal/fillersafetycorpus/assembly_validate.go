package fillersafetycorpus

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func validateReviewDraftConfig(config ReviewDraftConfig) error {
	if strings.TrimSpace(config.PlanPath) == "" || strings.TrimSpace(config.InputRoot) == "" ||
		strings.TrimSpace(config.OutputDirectory) == "" {
		return fmt.Errorf("spoken corpus assembly requires a plan, private input root, and new output directory")
	}
	return nil
}

func validateAssemblyPlan(plan AssemblyPlan) error {
	if plan.SchemaVersion != AssemblyPlanSchemaVersion || plan.ContractVersion != AssemblyPlanContractVersion ||
		plan.AssembledAt.IsZero() || plan.ChallengeKind != fillersafetycert.ChallengeCertification ||
		!validFileAuthority(plan.Policy) || plan.Policy.Bytes > maximumAssemblyPolicyBytes ||
		!validSHA256(plan.ProposerSHA256) || !boundedID(plan.ProposerFamily) ||
		!boundedID(plan.Implementation) || len(plan.Cohorts) == 0 || plan.ExpectedCases <= 0 ||
		plan.MaximumInputBytes <= 0 || plan.MaximumOutputBytes <= 0 || plan.MaximumWallTimeMS <= 0 ||
		plan.MaximumWallTimeMS > math.MaxInt64/int64(time.Millisecond) {
		return fmt.Errorf("spoken corpus assembly plan identity, draft envelope, or ceilings are invalid")
	}
	previous := ""
	expectedCases := 0
	for _, cohort := range plan.Cohorts {
		if !validRelative(cohort.CohortPath) || !validRelative(cohort.SourceRoot) || cohort.CohortPath <= previous ||
			!validSHA256(cohort.SHA256) || (cohort.Kind != PreparedCohortKindCleanCandidate &&
			cohort.Kind != PreparedCohortKindPositiveCandidate) || !boundedID(cohort.Dataset) ||
			cohort.ExpectedCases <= 0 || cohort.MaximumBytes <= 0 ||
			!pathWithinRelativeRoot(cohort.SourceRoot, cohort.CohortPath) {
			return fmt.Errorf("spoken corpus assembly contains an invalid, unsorted, or unbounded cohort")
		}
		if cohort.ExpectedCases > plan.ExpectedCases-expectedCases {
			return fmt.Errorf("spoken corpus assembly cohort counts exceed the exact combined count")
		}
		expectedCases += cohort.ExpectedCases
		previous = cohort.CohortPath
	}
	if expectedCases != plan.ExpectedCases {
		return fmt.Errorf("spoken corpus assembly cohort counts do not equal the exact combined count")
	}
	return nil
}

func pathWithinRelativeRoot(root, path string) bool {
	relative, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(path))
	return err == nil && filepath.IsLocal(relative)
}

func preparedLabel(claim string) (string, bool) {
	switch claim {
	case PreparedCohortKindCleanCandidate:
		return fillersafetycert.LabelClean, true
	case PreparedCohortKindPositiveCandidate:
		return fillersafetycert.LabelPositive, true
	default:
		return "", false
	}
}

func validateReviewWorklist(worklist ReviewWorklist, draft fillersafetycert.AuthorityDraft, draftSHA string) error {
	if worklist.SchemaVersion != ReviewWorklistSchemaVersion || worklist.ContractVersion != ReviewWorklistContractVersion ||
		worklist.AssembledAt.IsZero() || worklist.DraftSHA256 != draftSHA || worklist.PolicyPath != "policy.json" ||
		worklist.PolicySHA256 != draft.PolicySHA256 || len(worklist.Cases) != len(draft.Cases) {
		return fmt.Errorf("spoken corpus review worklist identity or exact case count is invalid")
	}
	for index, item := range worklist.Cases {
		draftCase := draft.Cases[index]
		authoritySHA, err := fillersafety.SourceAuthoritySHA256(draftCase.SourceAuthority)
		validTranscript := item.TranscriptPath == "" && item.TranscriptSHA256 == "" && item.TranscriptBytes == 0
		validTranscript = validTranscript || (item.TranscriptPath == "cases/"+item.CaseID+"/transcript.txt" &&
			validSHA256(item.TranscriptSHA256) && item.TranscriptBytes > 0)
		intervals := make([]PreparedPositiveInterval, len(draftCase.PositiveIntervals))
		for intervalIndex, interval := range draftCase.PositiveIntervals {
			intervals[intervalIndex] = PreparedPositiveInterval{RuleID: interval.RuleID, StartMS: interval.StartMS, EndMS: interval.EndMS}
		}
		if err != nil || item.CaseID != draftCase.CaseID || item.SourcePath != draftCase.SourcePath ||
			item.SourceSHA256 != draftCase.SourceAuthority.SourceSHA256 || item.SourceAuthoritySHA256 != authoritySHA ||
			item.SourceBytes != draftCase.SourceAuthority.SourceBytes || item.DurationMS != draftCase.SourceAuthority.DurationMS ||
			!validTranscript || item.TruthProvenancePath != draftCase.TruthProvenancePath ||
			item.TruthProvenanceSHA256 != draftCase.TruthProvenanceSHA256 || item.TruthProvenanceBytes <= 0 ||
			item.RightsPath != draftCase.RightsPath || item.RightsSHA256 != draftCase.RightsSHA256 || item.RightsBytes <= 0 ||
			item.Claim != draftCase.Label ||
			item.Locale != draftCase.Locale || !slices.Equal(item.Slices, draftCase.Slices) ||
			!slices.Equal(item.PositiveIntervals, intervals) {
			return fmt.Errorf("spoken corpus review worklist contains a mismatched case")
		}
	}
	return nil
}
