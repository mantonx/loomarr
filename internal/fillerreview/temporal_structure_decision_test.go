package fillerreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
)

func TestPublishTemporalStructureDecisionsConfirmsAgreementWithoutPrivateTruth(t *testing.T) {
	fixture := newTemporalStructureComparisonFixture(t)
	first := fixture.assessmentSet("assessor-a", "gemini", "google/model")
	second := fixture.assessmentSet("assessor-b", "seed", "bytedance/model")
	compilation := temporalStructureAssessmentByTruth(&second, fillereval.UnitCompilation)
	originalBoundary := compilation.Segments[0].EndMS
	moveTemporalStructureTestBoundary(compilation, 1_000)

	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", first)
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", second)
	if err := os.Remove(fixture.authorityPath); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "decisions.json")
	report, digest, err := PublishTemporalStructureDecisions(fixture.decisionConfig(outputPath, firstPath, secondPath))
	if err != nil {
		t.Fatal(err)
	}
	if !reviewSHA256(digest) || report.AssessmentMediaProfileSHA256 != fillerstructuremedia.CanonicalProfile().SHA256 || report.Cases != 3 || report.ConfirmedCases != 3 || report.HeldCases != 0 || report.IndependentModelFamilies != 2 || report.ProductionAdmissionAllowed || len(report.HoldReasons) != 0 {
		t.Fatalf("decision report = %+v digest=%q", report, digest)
	}
	decision := temporalStructureDecisionByUnit(t, report, fillereval.UnitCompilation)
	if decision.Status != TemporalStructureDecisionConfirmed || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != temporalStructureDecisionReasonAgreement || len(decision.Segments) != 2 || decision.Segments[0].EndMS != originalBoundary+500 || decision.Segments[1].StartMS != originalBoundary+500 {
		t.Fatalf("compilation decision = %+v", decision)
	}
	if decision.Segments[0].Disposition != TemporalStructureDispositionFillerCandidate || decision.Segments[1].Disposition != TemporalStructureDispositionFillerCandidate {
		t.Fatalf("compilation dispositions = %+v", decision.Segments)
	}
	if _, _, err := PublishTemporalStructureDecisions(fixture.decisionConfig(outputPath, firstPath, secondPath)); err == nil {
		t.Fatal("immutable decision output was overwritten")
	}
}

func TestPublishTemporalStructureDecisionsHoldsEveryConflictClass(t *testing.T) {
	fixture := newTemporalStructureComparisonFixture(t)
	first := fixture.assessmentSet("assessor-a", "gemini", "google/model")
	second := fixture.assessmentSet("assessor-b", "seed", "bytedance/model")

	standalone := temporalStructureAssessmentByTruth(&second, fillereval.UnitStandalone)
	if standalone.Role.Kind == fillereval.TemporalRoleCommercial {
		standalone.Role.Kind = fillereval.TemporalRolePromo
		standalone.Segments[0].Role = fillereval.TemporalSegmentPromo
	} else {
		standalone.Role.Kind = fillereval.TemporalRoleCommercial
		standalone.Segments[0].Role = fillereval.TemporalSegmentCommercial
	}
	compilation := temporalStructureAssessmentByTruth(&second, fillereval.UnitCompilation)
	moveTemporalStructureTestBoundary(compilation, TemporalStructureNearBoundaryMS+1)
	excerpt := temporalStructureAssessmentByTruth(&second, fillereval.UnitProgrammeExcerpt)
	excerpt.Unit = nil
	excerpt.Segments = nil
	excerpt.OperationalFailure = &fillereval.TemporalOperationalFailure{Code: fillereval.TemporalFailureTimeout, Detail: "deadline", Retryable: true}
	excerpt.Inference.Calls[0].ResponseSHA256 = ""
	excerpt.Inference.Calls[0].OperationalFailure = fillereval.TemporalFailureTimeout

	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", first)
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", second)
	report, _, err := PublishTemporalStructureDecisions(fixture.decisionConfig(filepath.Join(t.TempDir(), "decisions.json"), firstPath, secondPath))
	if err != nil {
		t.Fatal(err)
	}
	if report.ConfirmedCases != 0 || report.HeldCases != 3 {
		t.Fatalf("decision totals = %+v", report)
	}
	wantReasons := map[string]bool{
		temporalStructureDecisionReasonRoleDisagreement:   false,
		temporalStructureDecisionReasonBoundary:           false,
		temporalStructureDecisionReasonOperationalFailure: false,
	}
	for _, summary := range report.HoldReasons {
		if _, wanted := wantReasons[summary.Reason]; wanted && summary.Cases == 1 {
			wantReasons[summary.Reason] = true
		}
	}
	for reason, found := range wantReasons {
		if !found {
			t.Fatalf("missing one-case hold reason %q in %+v", reason, report.HoldReasons)
		}
	}
	for _, decision := range report.Decisions {
		if decision.Status != TemporalStructureDecisionHeld || decision.Unit != "" || decision.Role != "" || len(decision.Segments) != 0 || len(decision.Candidates) != 2 {
			t.Fatalf("held decision leaked an actionable plan: %+v", decision)
		}
	}
}

func TestTemporalStructureDecisionCountsOneVotePerModelFamily(t *testing.T) {
	fixture := newTemporalStructureComparisonFixture(t)
	first := fixture.assessmentSet("assessor-a", "same-family", "model/route-a")
	second := fixture.assessmentSet("assessor-b", "same-family", "model/route-b")
	third := fixture.assessmentSet("assessor-c", "independent-family", "other/model")
	compilation := temporalStructureAssessmentByTruth(&second, fillereval.UnitCompilation)
	originalBoundary := compilation.Segments[0].EndMS
	moveTemporalStructureTestBoundary(compilation, TemporalStructureNearBoundaryMS)

	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", first)
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", second)
	thirdPath := writeTemporalHumanJSON(t, t.TempDir(), "third.json", third)
	config := fixture.decisionConfig(filepath.Join(t.TempDir(), "two-family.json"), firstPath, secondPath, thirdPath)
	report, _, err := PublishTemporalStructureDecisions(config)
	if err != nil {
		t.Fatal(err)
	}
	decision := temporalStructureDecisionByUnit(t, report, fillereval.UnitCompilation)
	if report.IndependentModelFamilies != 2 || decision.Segments[0].EndMS != originalBoundary+500 {
		t.Fatalf("same-family routes were not collapsed to one vote: report=%+v decision=%+v", report, decision)
	}

	config = fixture.decisionConfig(filepath.Join(t.TempDir(), "one-family.json"), firstPath, secondPath)
	if _, _, err := PublishTemporalStructureDecisions(config); err == nil || !strings.Contains(err.Error(), "two independent model families") {
		t.Fatalf("one-family error = %v", err)
	}
}

func (fixture temporalStructureComparisonFixture) decisionConfig(outputPath string, paths ...string) TemporalStructureDecisionConfig {
	return TemporalStructureDecisionConfig{
		PublicManifestPath: fixture.manifestPath, PrivateAuthoritySHA256: fixture.authoritySHA,
		AssessmentPaths: paths, ExpectedCases: len(fixture.manifest.Cases),
		DecidedAt: fixture.manifest.GeneratedAt.Add(4 * time.Hour), OutputPath: outputPath,
	}
}

func moveTemporalStructureTestBoundary(assessment *TemporalStructureAssessment, deltaMS int64) {
	assessment.Segments[0].EndMS += deltaMS
	assessment.Segments[1].StartMS += deltaMS
	for index, atMS := range assessment.Segments[1].DecisiveAtMS {
		assessment.Segments[1].DecisiveAtMS[index] = atMS + deltaMS
	}
}

func temporalStructureDecisionByUnit(t *testing.T, report TemporalStructureDecisionReport, unit fillereval.UnitKind) TemporalStructureCaseDecision {
	t.Helper()
	for _, decision := range report.Decisions {
		if decision.Unit == unit {
			return decision
		}
	}
	t.Fatalf("decision unit %q not found in %+v", unit, report.Decisions)
	return TemporalStructureCaseDecision{}
}
