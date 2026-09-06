package fillersafetycorpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func TestAssembleReviewDraftPublishesOneSelfContainedLockerCompatibleCorpus(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	result, err := AssembleReviewDraft(t.Context(), ReviewDraftConfig{
		PlanPath: fixture.planPath, InputRoot: fixture.root, OutputDirectory: fixture.output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != 162 || result.PositiveFamilies != 59 || result.CleanFamilies != 103 ||
		!validSHA256(result.DraftSHA256) || !validSHA256(result.WorklistSHA256) {
		t.Fatalf("result=%+v", result)
	}
	draftPath := filepath.Join(fixture.output, "draft.json")
	draft := readAssemblyJSON[fillersafetycert.AuthorityDraft](t, draftPath)
	draftRaw, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatal(err)
	}
	canonical, canonicalSHA, err := fillersafetycert.MarshalCertificationDraft(draft)
	if err != nil || string(canonical) != string(draftRaw) || canonicalSHA != result.DraftSHA256 {
		t.Fatalf("assembled draft is not canonical: sha=%s canonical=%s err=%v", result.DraftSHA256, canonicalSHA, err)
	}
	firstWorklist, err := os.ReadFile(filepath.Join(fixture.output, "primary-review-one.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondWorklist, err := os.ReadFile(filepath.Join(fixture.output, "primary-review-two.json"))
	if err != nil {
		t.Fatal(err)
	}
	worklist := readAssemblyJSON[ReviewWorklist](t, filepath.Join(fixture.output, "primary-review-one.json"))
	if string(firstWorklist) != string(secondWorklist) || bytesSHA(firstWorklist) != result.WorklistSHA256 ||
		worklist.DraftSHA256 != result.DraftSHA256 || worklist.PolicySHA256 != fixture.plan.Policy.SHA256 ||
		len(worklist.Cases) != len(draft.Cases) {
		t.Fatalf("worklist does not bind assembled draft: worklist=%+v result=%+v", worklist, result)
	}
	for _, forbidden := range []string{"reviewerId", "decision", "outcome", "evaluation"} {
		if strings.Contains(string(firstWorklist), forbidden) {
			t.Fatalf("worklist contains completed review or evaluation field %q", forbidden)
		}
	}
	actualOutputBytes := int64(0)
	if err := filepath.Walk(fixture.output, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("directory %s mode=%o", path, info.Mode().Perm())
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("file %s mode=%v", path, info.Mode())
		}
		actualOutputBytes += info.Size()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if actualOutputBytes != result.OutputBytes {
		t.Fatalf("output bytes=%d reported=%d", actualOutputBytes, result.OutputBytes)
	}

	firstReview := fixtureAuthorityReviewFromDraft(draft, result.DraftSHA256, "reviewer-one", fixture.plan.AssembledAt.Add(time.Hour))
	secondReview := fixtureAuthorityReviewFromDraft(draft, result.DraftSHA256, "reviewer-two", fixture.plan.AssembledAt.Add(2*time.Hour))
	firstReviewPath, secondReviewPath := filepath.Join(fixture.root, "first-review.json"), filepath.Join(fixture.root, "second-review.json")
	writePrivateJSONFixture(t, firstReviewPath, firstReview)
	writePrivateJSONFixture(t, secondReviewPath, secondReview)
	seedPath := filepath.Join(fixture.root, "locker-seed.bin")
	if err := os.WriteFile(seedPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked, err := fillersafetycert.BuildAuthority(t.Context(), fillersafetycert.AuthorityBuildConfig{
		DraftPath: draftPath, FirstReviewPath: firstReviewPath, SecondReviewPath: secondReviewPath,
		SeedPath: seedPath, SourceRoot: fixture.output, AuthoredAt: fixture.plan.AssembledAt.Add(3 * time.Hour),
		ExpectedCases: len(draft.Cases), MaximumSourceBytes: 64 << 20,
		ValidateEvidence: func(_, _ []byte, item fillersafetycert.AuthorityDraftCase, _ time.Time) error {
			if item.SourceAuthority.SourceID != item.CaseID {
				return fmt.Errorf("fixture source is unbound")
			}
			return nil
		},
		OutputPath: filepath.Join(fixture.root, "locked-authority.json"),
	})
	if err != nil || locked.PositiveFamilies != 59 || locked.CleanFamilies != 103 {
		t.Fatalf("assembled draft did not pass authority locker: result=%+v err=%v", locked, err)
	}
}

func TestAssembleReviewDraftIsByteReproducible(t *testing.T) {
	t.Parallel()
	fixture := newAssemblyFixture(t)
	first, err := AssembleReviewDraft(t.Context(), ReviewDraftConfig{
		PlanPath: fixture.planPath, InputRoot: fixture.root, OutputDirectory: fixture.output,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondOutput := filepath.Join(fixture.root, "assembled-second")
	second, err := AssembleReviewDraft(t.Context(), ReviewDraftConfig{
		PlanPath: fixture.planPath, InputRoot: fixture.root, OutputDirectory: secondOutput,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"draft.json", "primary-review-one.json", "primary-review-two.json"} {
		firstRaw, firstErr := os.ReadFile(filepath.Join(fixture.output, name))
		secondRaw, secondErr := os.ReadFile(filepath.Join(secondOutput, name))
		if firstErr != nil || secondErr != nil || string(firstRaw) != string(secondRaw) {
			t.Fatalf("%s differs: %v/%v", name, firstErr, secondErr)
		}
	}
	if first.DraftSHA256 != second.DraftSHA256 || first.WorklistSHA256 != second.WorklistSHA256 ||
		first.InputBytes != second.InputBytes || first.OutputBytes != second.OutputBytes {
		t.Fatalf("results differ: first=%+v second=%+v", first, second)
	}
}

func fixtureAuthorityReviewFromDraft(
	draft fillersafetycert.AuthorityDraft,
	draftSHA, reviewerID string,
	submittedAt time.Time,
) fillersafetycert.AuthorityReview {
	review := fillersafetycert.AuthorityReview{
		SchemaVersion:   fillersafetycert.AuthorityReviewSchemaVersion,
		ContractVersion: fillersafetycert.AuthorityReviewContractVersion,
		DraftSHA256:     draftSHA, ReviewerID: reviewerID, Role: fillersafetycert.ReviewerPrimary,
		Method:         fillersafetycert.ReviewerHuman,
		EvidenceSHA256: hashBytes([]byte("review evidence:" + reviewerID)), SubmittedAt: submittedAt,
	}
	for _, item := range draft.Cases {
		review.Assessments = append(review.Assessments, fillersafetycert.ReviewAssessment{
			CaseID: item.CaseID, Decision: fillersafetycert.ReviewDecisionVerified,
			PositiveIntervals: item.PositiveIntervals,
		})
	}
	return review
}
