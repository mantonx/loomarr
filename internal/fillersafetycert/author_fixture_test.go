package fillersafetycert

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

type authorityBuildFixture struct {
	root, draftPath, firstPath, secondPath, adjudicatorPath, seedPath, outputPath string
	draft                                                                         AuthorityDraft
	first, second                                                                 AuthorityReview
	adjudicator                                                                   AuthorityReview
	config                                                                        AuthorityBuildConfig
}

func newAuthorityBuildFixture(t *testing.T) *authorityBuildFixture {
	t.Helper()
	root := t.TempDir()
	measuredAt := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	submittedAt := measuredAt.Add(time.Hour)
	authoredAt := submittedAt.Add(time.Hour)
	draft := AuthorityDraft{
		SchemaVersion: AuthorityDraftSchemaVersion, ContractVersion: AuthorityDraftContractVersion,
		ChallengeKind: ChallengeCertification, PolicySHA256: fixtureSHA(10), ProposerSHA256: fixtureSHA(11),
		ProposerFamily: "complete-audio-window-v1", Implementation: "spoken-safety-evaluator-v1",
		AudioRoute: fixtureRoute([]string{"audio"}, "native-audio", 20),
		VideoRoute: fixtureRoute([]string{"audio", "video"}, "complete-video", 30),
	}
	positiveSlices := requiredPositiveSlices()
	for index := 0; index < MinimumPositiveFamilies; index++ {
		draft.Cases = append(draft.Cases, fixtureDraftCase(t, root, measuredAt, index, LabelPositive, []string{positiveSlices[index%len(positiveSlices)]}))
	}
	cleanSlices := requiredCleanSlices()
	for index := range MinimumCleanFamilies {
		draft.Cases = append(draft.Cases, fixtureDraftCase(t, root, measuredAt, MinimumPositiveFamilies+index, LabelClean, []string{cleanSlices[index%len(cleanSlices)]}))
	}
	draftPath := filepath.Join(root, "draft.json")
	writeFixtureJSON(t, draftPath, draft)
	draftRaw, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatal(err)
	}
	draftSHA := hashBytes(draftRaw)
	first := fixtureAuthorityReview(draft, draftSHA, "reviewer-one", submittedAt)
	second := fixtureAuthorityReview(draft, draftSHA, "reviewer-two", submittedAt.Add(time.Minute))
	firstPath, secondPath := filepath.Join(root, "first.json"), filepath.Join(root, "second.json")
	writeFixtureJSON(t, firstPath, first)
	writeFixtureJSON(t, secondPath, second)
	seedPath := filepath.Join(root, "seed.bin")
	if err := os.WriteFile(seedPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &authorityBuildFixture{
		root: root, draftPath: draftPath, firstPath: firstPath, secondPath: secondPath,
		adjudicatorPath: filepath.Join(root, "adjudicator.json"), seedPath: seedPath,
		outputPath: filepath.Join(root, "authority.json"), draft: draft, first: first, second: second,
	}
	fixture.config = AuthorityBuildConfig{
		DraftPath: draftPath, FirstReviewPath: firstPath, SecondReviewPath: secondPath,
		SeedPath: seedPath, SourceRoot: root, AuthoredAt: authoredAt,
		ExpectedCases: len(draft.Cases), MaximumSourceBytes: 1 << 20,
		ValidateEvidence: func(rightsRaw, provenanceRaw []byte, item AuthorityDraftCase, _ time.Time) error {
			if string(rightsRaw) != "private rights evidence" || string(provenanceRaw) != "private truth provenance" ||
				item.SourceAuthority.SourceID != item.CaseID {
				return fmt.Errorf("unexpected fixture evidence")
			}
			return nil
		},
		OutputPath: fixture.outputPath,
	}
	return fixture
}

func fixtureDraftCase(t *testing.T, root string, measuredAt time.Time, index int, label string, slices []string) AuthorityDraftCase {
	t.Helper()
	id := fmt.Sprintf("case-%03d", index+1)
	relative := filepath.ToSlash(filepath.Join("sources", id+".mp4"))
	if err := os.MkdirAll(filepath.Join(root, "sources"), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte(fmt.Sprintf("complete audiovisual fixture %03d", index+1))
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if index == 0 {
		if err := os.WriteFile(filepath.Join(root, "truth.bin"), []byte("private truth provenance"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "rights.bin"), []byte("private rights evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(contents))
	item := AuthorityDraftCase{
		CaseID: id, SourcePath: relative, SourceFamily: fmt.Sprintf("speaker-%03d", index+1),
		TruthProvenancePath: "truth.bin", TruthProvenanceSHA256: hashBytes([]byte("private truth provenance")),
		RightsPath: "rights.bin", RightsSHA256: hashBytes([]byte("private rights evidence")),
		Label: label, Locale: "en-US", Slices: slices,
		SourceAuthority: fillersafety.SourceAuthority{
			SchemaVersion: fillersafety.SourceAuthoritySchemaVersion, PolicySHA256: fixtureSHA(10),
			Implementation: "spoken-safety-evaluator-v1", SourceID: id, SourceSHA256: digest,
			SourceBytes: int64(len(contents)), DurationMS: 3_000, HasAudio: true, HasVideo: true,
			MeasuredAt: measuredAt, FFmpeg: fillersafety.ToolIdentity{Version: "7.1", BinarySHA256: fixtureSHA(40)},
			FFprobe: fillersafety.ToolIdentity{Version: "7.1", BinarySHA256: fixtureSHA(41)},
		},
	}
	if label == LabelPositive {
		item.PositiveIntervals = []PositiveInterval{{RuleID: fixtureRuleID, StartMS: 500, EndMS: 1_500}}
	}
	return item
}

func fixtureAuthorityReview(draft AuthorityDraft, draftSHA, reviewerID string, submittedAt time.Time) AuthorityReview {
	review := AuthorityReview{
		SchemaVersion: AuthorityReviewSchemaVersion, ContractVersion: AuthorityReviewContractVersion,
		DraftSHA256: draftSHA, ReviewerID: reviewerID, Role: ReviewerPrimary, Method: ReviewerHuman,
		EvidenceSHA256: hashBytes([]byte("review evidence:" + reviewerID)), SubmittedAt: submittedAt,
	}
	for _, item := range draft.Cases {
		review.Assessments = append(review.Assessments, ReviewAssessment{
			CaseID: item.CaseID, Decision: ReviewDecisionVerified,
			PositiveIntervals: append([]PositiveInterval(nil), item.PositiveIntervals...),
		})
	}
	return review
}

func attachFixtureModelEvidence(review *AuthorityReview, modelFamily string) {
	review.Method = ReviewerModel
	review.ModelFamily = modelFamily
	evidence := ModelReviewEvidence{
		SchemaVersion: ModelReviewEvidenceSchemaVersion, ContractVersion: ModelReviewEvidenceContractVersion,
		PlanSHA256: fixtureSHA(8100), WorklistSHA256: fixtureSHA(8101), PolicySHA256: fixtureSHA(10),
		SnapshotSHA256: fixtureSHA(8102), RequestedModel: "vendor/reviewer-model",
		ResolvedModel: "vendor/reviewer-model-2026", UpstreamProvider: "provider",
		UpstreamProviderSlug: "provider", DisableReasoning: true,
		ModelFamily: modelFamily, PromptSHA256: fixtureSHA(8103),
		SchemaSHA256: fixtureSHA(8104),
		FFmpeg:       fillersafety.ToolIdentity{Version: "ffmpeg 7.1", BinarySHA256: fixtureSHA(8105)},
		StartedAt:    review.SubmittedAt.Add(-2 * time.Minute), CompletedAt: review.SubmittedAt,
		MaximumRequests: len(review.Assessments), MaximumChargeNanoUSD: 1_000,
		MaximumSpendNanoUSD: int64(len(review.Assessments)) * 1_000,
	}
	for index, assessment := range review.Assessments {
		evidence.Attempts = append(evidence.Attempts, ModelReviewAttemptEvidence{
			CaseID: assessment.CaseID, Attempt: 1, RequestedAt: review.SubmittedAt.Add(-time.Minute),
			ReviewedAt: review.SubmittedAt.Add(-30 * time.Second), RequestSHA256: fixtureSHA(8200 + index*3),
			ResponseSHA256: fixtureSHA(8201 + index*3), GenerationID: fmt.Sprintf("generation-%d", index+1),
			State: ModelReviewAttemptAccepted, ObservationSHA256: fixtureSHA(8202 + index*3),
			PromptTokens: 10, CompletionTokens: 2, ChargedNanoUSD: 100,
		})
		evidence.PromptTokens += 10
		evidence.CompletionTokens += 2
		evidence.ChargedNanoUSD += 100
	}
	evidence.Requests = len(evidence.Attempts)
	review.ModelEvidence = &evidence
	review.EvidenceSHA256, _ = ModelReviewEvidenceSHA256(evidence)
}

func (fixture *authorityBuildFixture) rewrite(t *testing.T) {
	t.Helper()
	writeFixtureJSON(t, fixture.draftPath, fixture.draft)
	raw, err := os.ReadFile(fixture.draftPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := hashBytes(raw)
	fixture.first.DraftSHA256, fixture.second.DraftSHA256 = digest, digest
	writeFixtureJSON(t, fixture.firstPath, fixture.first)
	writeFixtureJSON(t, fixture.secondPath, fixture.second)
	if fixture.config.AdjudicatorPath != "" {
		fixture.adjudicator.DraftSHA256 = digest
		writeFixtureJSON(t, fixture.adjudicatorPath, fixture.adjudicator)
	}
}
