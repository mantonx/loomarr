package fillersafetycorpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func TestPrepareKnownScriptFeedsVCTKReviewDraftAndAuthorityLock(t *testing.T) {
	t.Parallel()
	preparedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	policy := fillersafety.Policy{
		SchemaVersion: fillersafety.PolicySchemaVersion, ContractVersion: fillersafety.PolicyContractVersion,
		PolicyID: "policy-known-script-fixture", GeneratedAt: preparedAt.Add(-6 * time.Hour),
		MaximumInterSegmentGapMS: 250,
		Rules: []fillersafety.PolicyRule{{
			ID: "rule-000000000000000000000001", Class: fillersafety.PolicyClassProhibited,
			MatchMode: fillersafety.PolicyModeExactWords, Variants: []string{"restricted token"},
		}},
	}
	policyRaw, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	policyRaw = append(policyRaw, '\n')
	fixture := newKnownScriptFixtureForPolicy(t, bytesSHA(policyRaw))
	fixture.output = filepath.Join(fixture.parent, "01-positive")
	fixture.config.OutputDirectory = fixture.output
	if _, err := prepareKnownScript(t.Context(), fixture.config, fixture.wrapper); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.parent, "policy.json"), policyRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	positive := readPreparedFixture[PreparedCohort](t, filepath.Join(fixture.output, "cohort.json"))
	vctkFixture := newVCTKFixture(t)
	vctkFixture.output = filepath.Join(fixture.parent, "02-vctk")
	vctkFixture.config.OutputDirectory = vctkFixture.output
	vctkFixture.config.PolicySHA256 = bytesSHA(policyRaw)
	vctkFixture.config.Implementation = positive.Cases[0].SourceAuthority.Implementation
	vctkFixture.config.PreparedAt = preparedAt
	if _, err := prepareVCTK(t.Context(), vctkFixture.config, vctkFixture.wrapper); err != nil {
		t.Fatal(err)
	}
	target := readPreparedFixture[PreparedCohort](t, filepath.Join(vctkFixture.output, "cohort.json"))
	otherSlices := []string{
		fillersafetycert.SliceMusicOnly,
		fillersafetycert.SliceNearMatch,
		fillersafetycert.SliceWordless,
	}
	other := makePreparedFixtureCohort(
		t, fixture.parent, "03-other-clean", PreparedCohortKindCleanCandidate, "curated-other-clean",
		len(otherSlices), preparedAt, bytesSHA(policyRaw),
		func(index int) []string { return []string{otherSlices[index]} },
	)
	cohorts := []struct {
		root   string
		cohort PreparedCohort
	}{
		{root: "01-positive", cohort: positive},
		{root: "02-vctk", cohort: target},
		{root: "03-other-clean", cohort: other},
	}
	assembledAt := preparedAt.Add(time.Hour)
	plan := AssemblyPlan{
		SchemaVersion: AssemblyPlanSchemaVersion, ContractVersion: AssemblyPlanContractVersion,
		AssembledAt: assembledAt, ChallengeKind: fillersafetycert.ChallengeCertification,
		Policy:         FileAuthority{Path: "policy.json", SHA256: bytesSHA(policyRaw), Bytes: int64(len(policyRaw))},
		ProposerSHA256: fixtureSHA(9_000), ProposerFamily: "complete-audio-window-proposer",
		Implementation:    "spoken-safety-evaluator-v1",
		AudioRoute:        assemblyFixtureRoute([]string{"audio"}, "native-audio", 9_100),
		VideoRoute:        assemblyFixtureRoute([]string{"audio", "video"}, "complete-video", 9_200),
		ExpectedCases:     len(positive.Cases) + len(target.Cases) + len(other.Cases),
		MaximumInputBytes: 64 << 20, MaximumOutputBytes: 64 << 20,
		MaximumWallTimeMS: int64(time.Minute / time.Millisecond),
	}
	for _, item := range cohorts {
		cohortPath := filepath.ToSlash(filepath.Join(item.root, "cohort.json"))
		raw, err := os.ReadFile(filepath.Join(fixture.parent, filepath.FromSlash(cohortPath)))
		if err != nil {
			t.Fatal(err)
		}
		plan.Cohorts = append(plan.Cohorts, AssemblyCohort{
			CohortPath: cohortPath, SourceRoot: item.root, SHA256: bytesSHA(raw),
			Kind: item.cohort.Kind, Dataset: item.cohort.Dataset, ExpectedCases: len(item.cohort.Cases),
			MaximumBytes: 32 << 20,
		})
	}
	planPath := filepath.Join(fixture.parent, "assembly-plan.json")
	writePrivateJSONFixture(t, planPath, plan)
	assembledRoot := filepath.Join(fixture.parent, "assembled")
	assembled, err := AssembleReviewDraft(t.Context(), ReviewDraftConfig{
		PlanPath: planPath, InputRoot: fixture.parent, OutputDirectory: assembledRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Cases != 162 || assembled.PositiveFamilies != 59 || assembled.CleanFamilies != 103 {
		t.Fatalf("assembled result=%+v", assembled)
	}

	worklist := readAssemblyJSON[ReviewWorklist](t, filepath.Join(assembledRoot, "primary-review-one.json"))
	foundAuthorizedPositive := false
	for _, item := range worklist.Cases {
		if item.Claim != fillersafetycert.LabelPositive {
			continue
		}
		rights := readAssemblyJSON[knownScriptRights](t, filepath.Join(assembledRoot, filepath.FromSlash(item.RightsPath)))
		if len(rights.ProcessorSchedule.Processors) == 1 && rights.ProcessorSchedule.Processors[0].ZDR {
			foundAuthorizedPositive = true
			break
		}
	}
	if !foundAuthorizedPositive {
		t.Fatal("assembled review worklist lacks an authorized known-script positive")
	}

	draftPath := filepath.Join(assembledRoot, "draft.json")
	draft := readAssemblyJSON[fillersafetycert.AuthorityDraft](t, draftPath)
	firstReview := fixtureAuthorityReviewFromDraft(draft, assembled.DraftSHA256, "reviewer-one", assembledAt.Add(time.Hour))
	secondReview := fixtureAuthorityReviewFromDraft(draft, assembled.DraftSHA256, "reviewer-two", assembledAt.Add(2*time.Hour))
	firstReviewPath := filepath.Join(fixture.parent, "first-review.json")
	secondReviewPath := filepath.Join(fixture.parent, "second-review.json")
	writePrivateJSONFixture(t, firstReviewPath, firstReview)
	writePrivateJSONFixture(t, secondReviewPath, secondReview)
	seedPath := filepath.Join(fixture.parent, "locker-seed.bin")
	if err := os.WriteFile(seedPath, []byte("abcdef0123456789abcdef0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidenceValidator := NewCertificationEvidenceValidator()
	locked, err := fillersafetycert.BuildAuthority(t.Context(), fillersafetycert.AuthorityBuildConfig{
		DraftPath: draftPath, FirstReviewPath: firstReviewPath, SecondReviewPath: secondReviewPath,
		SeedPath: seedPath, SourceRoot: assembledRoot, AuthoredAt: assembledAt.Add(3 * time.Hour),
		ExpectedCases: len(draft.Cases), MaximumSourceBytes: 64 << 20,
		ValidateEvidence: func(rightsRaw, provenanceRaw []byte, item fillersafetycert.AuthorityDraftCase, at time.Time) error {
			if item.Label == fillersafetycert.LabelClean &&
				(len(item.Slices) != 1 || item.Slices[0] != fillersafetycert.SliceTargetLocale) {
				return nil // Synthetic clean cohorts exercise assembly, not a supported rights contract.
			}
			return evidenceValidator.Validate(rightsRaw, provenanceRaw, item, at)
		},
		OutputPath: filepath.Join(fixture.parent, "locked-authority.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if locked.PositiveFamilies != 59 || locked.CleanFamilies != 103 || !validSHA256(locked.AuthoritySHA256) {
		t.Fatalf("locked result=%+v", locked)
	}
}
