package fillersafetycert

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildAuthorityPublishesOpaqueVerifiedAuthority(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	fixtureValidator := fixture.config.ValidateEvidence
	validatedCases := make(map[string]struct{}, len(fixture.draft.Cases))
	fixture.config.ValidateEvidence = func(rightsRaw, provenanceRaw []byte, item AuthorityDraftCase, at time.Time) error {
		if _, duplicate := validatedCases[item.CaseID]; duplicate {
			t.Fatalf("case evidence validated twice: %s", item.CaseID)
		}
		validatedCases[item.CaseID] = struct{}{}
		return fixtureValidator(rightsRaw, provenanceRaw, item, at)
	}

	result, err := BuildAuthority(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	authority, raw, err := readPrivateJSON[Authority](fixture.outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != len(fixture.draft.Cases) || result.PositiveFamilies != MinimumPositiveFamilies ||
		result.CleanFamilies != MinimumCleanFamilies || result.AuthoritySHA256 != hashBytes(raw) ||
		authority.CorpusManifestSHA256 != fixture.first.DraftSHA256 || len(authority.Cases) != result.Cases ||
		len(validatedCases) != len(fixture.draft.Cases) {
		t.Fatalf("result=%+v authority=%+v", result, authority)
	}
	text := string(raw)
	for _, private := range []string{"case-001", "speaker-001", "sources/", "reviewer-one", "truth.bin", "rights.bin"} {
		if strings.Contains(text, private) {
			t.Fatalf("authority leaked private value %q", private)
		}
	}
	info, err := os.Stat(fixture.outputPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("output info=%v err=%v", info, err)
	}
}

func TestBuildAuthorityIsByteReproducible(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	secondOutput := fixture.root + "/authority-second.json"

	first, err := BuildAuthority(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := fixture.config
	secondConfig.OutputPath = secondOutput
	second, err := BuildAuthority(t.Context(), secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	firstRaw, firstErr := os.ReadFile(fixture.outputPath)
	secondRaw, secondErr := os.ReadFile(secondOutput)
	if firstErr != nil || secondErr != nil || first.AuthoritySHA256 != second.AuthoritySHA256 || string(firstRaw) != string(secondRaw) {
		t.Fatalf("first=%+v second=%+v read_errs=%v/%v", first, second, firstErr, secondErr)
	}
}

func TestBuildAuthorityRejectsChangedSourceBytesWithoutPublishing(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	path := fixture.root + "/" + fixture.draft.Cases[0].SourcePath
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := BuildAuthority(t.Context(), fixture.config); err == nil || !strings.Contains(err.Error(), "source bytes") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(fixture.outputPath); !os.IsNotExist(err) {
		t.Fatalf("output exists after failure: %v", err)
	}
}

func TestBuildAuthorityRejectsEvidenceChangedAfterReview(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	if err := os.WriteFile(fixture.root+"/truth.bin", []byte("post-review mutation"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := BuildAuthority(t.Context(), fixture.config); err == nil || !strings.Contains(err.Error(), "truth provenance") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(fixture.outputPath); !os.IsNotExist(err) {
		t.Fatalf("output exists after failure: %v", err)
	}
}

func TestBuildAuthorityRejectsUnboundRightsBeforeSourcePlanning(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	var calls atomic.Int64
	fixture.config.ValidateEvidence = func(rightsRaw, provenanceRaw []byte, item AuthorityDraftCase, at time.Time) error {
		calls.Add(1)
		if string(rightsRaw) != "private rights evidence" || string(provenanceRaw) != "private truth provenance" ||
			item.CaseID != fixture.draft.Cases[0].CaseID || !at.Equal(fixture.config.AuthoredAt) {
			t.Fatalf("rights=%q provenance=%q case=%q at=%v", rightsRaw, provenanceRaw, item.CaseID, at)
		}
		return fmt.Errorf("rights do not bind source")
	}
	sourcePath := fixture.root + "/" + fixture.draft.Cases[0].SourcePath
	if err := os.WriteFile(sourcePath, []byte("changed source that must not be planned"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := BuildAuthority(t.Context(), fixture.config)
	if err == nil || !strings.Contains(err.Error(), "do not authorize") || strings.Contains(err.Error(), "bind source") {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("rights validation calls=%d", calls.Load())
	}
	if _, statErr := os.Stat(fixture.outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("output exists after rights failure: %v", statErr)
	}
}

func TestBuildAuthorityRequiresEvidenceValidator(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	fixture.config.ValidateEvidence = nil

	if _, err := BuildAuthority(t.Context(), fixture.config); err == nil || !strings.Contains(err.Error(), "rights and provenance validation") {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildAuthorityUsesAdjudicationOnlyForDisagreement(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	fixture.first.Assessments[0].Decision = ReviewDecisionRejected
	fixture.first.Assessments[0].PositiveIntervals = nil
	fixture.adjudicator = AuthorityReview{
		SchemaVersion: AuthorityReviewSchemaVersion, ContractVersion: AuthorityReviewContractVersion,
		ReviewerID: "reviewer-three", Role: ReviewerAdjudicator, Method: ReviewerHuman,
		EvidenceSHA256: hashBytes([]byte("review evidence:reviewer-three")),
		SubmittedAt:    fixture.config.AuthoredAt.Add(-time.Minute),
		Assessments: []ReviewAssessment{{
			CaseID: fixture.draft.Cases[0].CaseID, Decision: ReviewDecisionVerified,
			PositiveIntervals: append([]PositiveInterval(nil), fixture.draft.Cases[0].PositiveIntervals...),
		}},
	}
	fixture.config.AdjudicatorPath = fixture.adjudicatorPath
	fixture.rewrite(t)

	if _, err := BuildAuthority(t.Context(), fixture.config); err != nil {
		t.Fatal(err)
	}
	authority, _, err := readPrivateJSON[Authority](fixture.outputPath)
	if err != nil {
		t.Fatal(err)
	}
	alias := opaqueID([]byte("0123456789abcdef0123456789abcdef"), "case", fixture.draft.Cases[0].CaseID, "sc-")
	for _, item := range authority.Cases {
		if item.Alias == alias && len(item.Reviewers) != 3 {
			t.Fatalf("disputed case reviewers=%+v", item.Reviewers)
		}
	}
}

func TestBuildAuthorityCanAdjudicateRejectedCleanControl(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	cleanIndex := MinimumPositiveFamilies
	fixture.first.Assessments[cleanIndex].Decision = ReviewDecisionRejected
	fixture.adjudicator = AuthorityReview{
		SchemaVersion: AuthorityReviewSchemaVersion, ContractVersion: AuthorityReviewContractVersion,
		ReviewerID: "reviewer-three", Role: ReviewerAdjudicator, Method: ReviewerHuman,
		EvidenceSHA256: hashBytes([]byte("review evidence:reviewer-three")),
		SubmittedAt:    fixture.config.AuthoredAt.Add(-time.Minute),
		Assessments: []ReviewAssessment{{
			CaseID: fixture.draft.Cases[cleanIndex].CaseID, Decision: ReviewDecisionVerified,
		}},
	}
	fixture.config.AdjudicatorPath = fixture.adjudicatorPath
	fixture.rewrite(t)

	if _, err := BuildAuthority(t.Context(), fixture.config); err != nil {
		t.Fatal(err)
	}
}

func TestBuildAuthorityRejectsCleanControlRejectedByBothPrimaries(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	cleanIndex := MinimumPositiveFamilies
	fixture.first.Assessments[cleanIndex].Decision = ReviewDecisionRejected
	fixture.second.Assessments[cleanIndex].Decision = ReviewDecisionRejected
	fixture.rewrite(t)

	if _, err := BuildAuthority(t.Context(), fixture.config); err == nil ||
		!strings.Contains(err.Error(), "reviews do not establish declared truth") {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildAuthorityRejectsRepeatedModelReviewerFamily(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	attachFixtureModelEvidence(&fixture.first, "review-model-family")
	attachFixtureModelEvidence(&fixture.second, "review-model-family")
	fixture.rewrite(t)

	if _, err := BuildAuthority(t.Context(), fixture.config); err == nil || !strings.Contains(err.Error(), "families are not independent") {
		t.Fatalf("err=%v", err)
	}
}
