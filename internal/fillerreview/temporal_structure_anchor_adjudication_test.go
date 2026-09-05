package fillerreview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestPublishTemporalStructureAnchorAdjudicationBurnsChallengeWithoutRepairingScore(t *testing.T) {
	fixture := newTemporalStructureAnchorAdjudicationFixture(t)
	output := filepath.Join(t.TempDir(), "authority.json")
	config := fixture.config(output)
	result, err := PublishTemporalStructureAnchorAdjudication(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != 1 || !reviewSHA256(result.AuthoritySHA256) {
		t.Fatalf("publish result = %+v", result)
	}
	authority := readStrictTestJSON[TemporalStructureAnchorAdjudicationAuthority](t, output)
	if authority.ChallengeDisposition != TemporalStructureBurnedDiagnosticOnly || authority.BlindHumanAuditRequired || authority.CertificationScoreRepairAllowed || authority.TrainingAllowed || authority.ProductionAdmissionAllowed {
		t.Fatalf("unsafe adjudication disposition = %+v", authority)
	}
	if authority.PriorExposure.Split != "holdout" || len(authority.PriorExposure.SourceSHA256) != 54 || len(authority.PriorExposure.FamilyIDs) != 12 || len(authority.PriorExposure.ProgrammeProvenance) != 6 {
		t.Fatalf("prior exposure was not preserved = %+v", authority.PriorExposure)
	}
	item := authority.Cases[0]
	if item.Alias != fixture.target.Alias || item.Coverage != TemporalStructureAnchorReviewComplete || item.Disposition != TemporalStructureAnchorConfirmed || item.Original != item.Adjudicated || item.EvidenceAlias == "" || item.CaseID == "" || item.SourceID == "" || item.FamilyID == "" || !reviewSHA256(item.SourceSHA256) {
		t.Fatalf("adjudicated target = %+v", item)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("authority mode = %o", info.Mode().Perm())
	}
	if _, err := PublishTemporalStructureAnchorAdjudication(config); err == nil {
		t.Fatal("immutable adjudication authority was overwritten")
	}
}

func TestValidateTemporalStructureAnchorAdjudicationRequiresExactCompleteReview(t *testing.T) {
	fixture := newTemporalStructureAnchorAdjudicationFixture(t)
	base := fixture.submission
	tests := []struct {
		name   string
		mutate func(*TemporalStructureAnchorAdjudicationSubmission)
		want   string
	}{
		{name: "missing target", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) { value.Cases = nil }, want: "exact canonical"},
		{name: "extra target", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) {
			value.Cases = append(value.Cases, value.Cases[0])
			value.Cases[1].Alias += "-extra"
		}, want: "exact canonical"},
		{name: "incomplete coverage", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) { value.Cases[0].Coverage = "contact_sheet" }, want: "complete audiovisual"},
		{name: "missing observations", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) {
			value.Cases[0].Observations = TemporalStructureAnchorObservations{}
		}, want: "opening"},
		{name: "unknown disposition", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) {
			value.Cases[0].Disposition = "model_majority"
		}, want: "closed value"},
		{name: "label drift while confirming", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) {
			value.Cases[0].Role = differentTemporalStructureRole(value.Cases[0].Role)
		}, want: "changes the original"},
		{name: "duplicate time", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) {
			value.Cases[0].DecisiveAtMS = []int64{100, 100}
		}, want: "canonical"},
		{name: "comparison digest", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) {
			value.ComparisonSHA256 = strings.Repeat("f", 64)
		}, want: "identity"},
		{name: "pre-comparison review", mutate: func(value *TemporalStructureAnchorAdjudicationSubmission) {
			value.ReviewedAt = fixture.comparison.ComparedAt.Add(-time.Second)
		}, want: "predates"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.Cases = append([]TemporalStructureAnchorAdjudicationSubmissionCase(nil), base.Cases...)
			value.Cases[0].DecisiveAtMS = append([]int64(nil), base.Cases[0].DecisiveAtMS...)
			test.mutate(&value)
			_, err := validateTemporalStructureAnchorAdjudicationSubmission(value, fixture.comparison, fixture.challenge, fixture.receipt, fixture.comparisonSHA, fixture.adjudicatedAt)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPublishTemporalStructureAnchorAdjudicationRejectsOpenedArtifactDrift(t *testing.T) {
	fixture := newTemporalStructureAnchorAdjudicationFixture(t)
	comparison := fixture.comparison
	comparison.AllAssessorsExactCorrect++
	tampered := writeTemporalHumanJSON(t, t.TempDir(), "comparison.json", comparison)
	config := fixture.config(filepath.Join(t.TempDir(), "authority.json"))
	config.ComparisonPath = tampered
	if _, err := PublishTemporalStructureAnchorAdjudication(config); err == nil || !strings.Contains(err.Error(), "exact reproduced") {
		t.Fatalf("comparison drift error = %v", err)
	}
}

type temporalStructureAnchorAdjudicationFixture struct {
	publicPath      string
	privatePath     string
	authoringPath   string
	receiptPath     string
	assessmentPaths []string
	comparisonPath  string
	submissionPath  string
	comparisonSHA   string
	receipt         TemporalStructureHoldoutReceipt
	challenge       TemporalStructureChallengeAuthority
	comparison      TemporalStructureComparisonReport
	target          TemporalStructureChallengeAuthorityCase
	submission      TemporalStructureAnchorAdjudicationSubmission
	adjudicatedAt   time.Time
}

func newTemporalStructureAnchorAdjudicationFixture(t *testing.T) temporalStructureAnchorAdjudicationFixture {
	t.Helper()
	source := newTemporalStructureHoldoutFixture(t)
	planRoot := filepath.Join(t.TempDir(), "plan")
	if _, err := BuildTemporalStructureHoldoutPlan(source.config(planRoot)); err != nil {
		t.Fatal(err)
	}
	authoringPath := filepath.Join(planRoot, "authoring.json")
	receiptPath := filepath.Join(planRoot, "receipt.json")
	authoring := readStrictTestJSON[TemporalStructureChallengeAuthoring](t, authoringPath)
	media := &fakeTemporalStructureMedia{durationByPath: make(map[string]int64, len(authoring.Sources))}
	for _, item := range authoring.Sources {
		media.durationByPath[filepath.Join(source.root, filepath.FromSlash(item.Path))] = item.DurationMS
	}
	challengeRoot := filepath.Join(t.TempDir(), "challenge")
	generatedAt := source.plannedAt.Add(time.Hour)
	if _, err := BuildTemporalStructureChallenge(context.Background(), TemporalStructureChallengeConfig{
		AuthoringPath: authoringPath, PlanReceiptPath: receiptPath, SourceRoot: source.root,
		OutputDir: challengeRoot, ChallengeID: "anchor-adjudication-fixture", Seed: "challenge-seed",
		GeneratedAt: generatedAt, Media: media,
	}); err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(challengeRoot, "public", "manifest.json")
	privatePath := filepath.Join(challengeRoot, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, publicPath)
	challenge := readStrictTestJSON[TemporalStructureChallengeAuthority](t, privatePath)
	publicSHA, err := hashFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	privateSHA, err := hashFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	first := temporalStructureAnchorAssessmentSet(manifest, challenge, publicSHA, privateSHA, "assessor-a", "qwen")
	second := temporalStructureAnchorAssessmentSet(manifest, challenge, publicSHA, privateSHA, "assessor-b", "claude")
	var target TemporalStructureChallengeAuthorityCase
	for _, item := range challenge.Cases {
		if item.Unit == fillereval.UnitStandalone {
			target = item
			break
		}
	}
	for index := range second.Assessments {
		if second.Assessments[index].Alias == target.Alias {
			second.Assessments[index].Role.Kind = differentTemporalStructureRole(target.Role)
		}
	}
	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", first)
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", second)
	comparedAt := generatedAt.Add(4 * time.Hour)
	comparisonPath := filepath.Join(t.TempDir(), "comparison.json")
	comparison, comparisonSHA, err := PublishTemporalStructureComparison(TemporalStructureComparisonConfig{
		PublicManifestPath: publicPath, PrivateAuthorityPath: privatePath,
		AssessmentPaths: []string{firstPath, secondPath}, ExpectedCases: TemporalStructureHoldoutCases,
		ComparedAt: comparedAt, OutputPath: comparisonPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.DiagnosticCandidates) != 1 || comparison.DiagnosticCandidates[0].Alias != target.Alias {
		t.Fatalf("fixture diagnostics = %+v", comparison.DiagnosticCandidates)
	}
	reviewedAt := comparedAt.Add(time.Hour)
	submission := TemporalStructureAnchorAdjudicationSubmission{
		SchemaVersion:   TemporalStructureAnchorAdjudicationSchemaVersion,
		ContractVersion: TemporalStructureAnchorAdjudicationSubmissionContract,
		ChallengeID:     challenge.ChallengeID, ComparisonSHA256: comparisonSHA,
		ReviewerID: "targeted-reviewer", ReviewedAt: reviewedAt,
		Cases: []TemporalStructureAnchorAdjudicationSubmissionCase{{
			Alias: target.Alias, Coverage: TemporalStructureAnchorReviewComplete,
			Observations: TemporalStructureAnchorObservations{
				Opening: "The item begins at its own presentation boundary.", InternalJoins: []TemporalStructureAnchorJoinObservation{},
				Closing: "The item reaches its own closing boundary before the file ends.",
			},
			Disposition: TemporalStructureAnchorConfirmed, Unit: target.Unit, Role: target.Role,
			DecisiveAtMS: []int64{100}, Rationale: "Complete playback confirms one independently bounded item and its original role.",
		}},
	}
	submissionPath := writeTemporalHumanJSON(t, t.TempDir(), "submission.json", submission)
	return temporalStructureAnchorAdjudicationFixture{
		publicPath: publicPath, privatePath: privatePath, authoringPath: authoringPath, receiptPath: receiptPath,
		assessmentPaths: []string{firstPath, secondPath}, comparisonPath: comparisonPath, submissionPath: submissionPath,
		comparisonSHA: comparisonSHA, receipt: readStrictTestJSON[TemporalStructureHoldoutReceipt](t, receiptPath),
		challenge: challenge, comparison: comparison, target: target, submission: submission,
		adjudicatedAt: reviewedAt.Add(time.Hour),
	}
}

func (fixture temporalStructureAnchorAdjudicationFixture) config(output string) TemporalStructureAnchorAdjudicationConfig {
	return TemporalStructureAnchorAdjudicationConfig{
		PublicManifestPath: fixture.publicPath, PrivateAuthorityPath: fixture.privatePath,
		PlanAuthoringPath: fixture.authoringPath, PlanReceiptPath: fixture.receiptPath,
		AssessmentPaths: fixture.assessmentPaths, ComparisonPath: fixture.comparisonPath,
		SubmissionPath: fixture.submissionPath, ExpectedCases: TemporalStructureHoldoutCases,
		AdjudicatedAt: fixture.adjudicatedAt, OutputPath: output,
	}
}

func temporalStructureAnchorAssessmentSet(
	manifest TemporalStructureChallengeManifest,
	challenge TemporalStructureChallengeAuthority,
	publicSHA, privateSHA, assessorID, family string,
) TemporalStructureAssessmentSet {
	completedAt := manifest.GeneratedAt.Add(3 * time.Hour)
	durations := make(map[string]int64, len(manifest.Cases))
	for _, item := range manifest.Cases {
		durations[item.Alias] = item.Video.DurationMS
	}
	set := TemporalStructureAssessmentSet{
		SchemaVersion: TemporalStructureAssessmentSchemaVersion, ContractVersion: TemporalStructureAssessmentContractVersion,
		ChallengeID: manifest.ChallengeID, PublicManifestSHA256: publicSHA, PrivateAuthoritySHA256: privateSHA,
		RawResultSHA256: strings.Repeat("a", 64), SnapshotFileSHA256: strings.Repeat("b", 64),
		CapabilitySnapshotSHA256: strings.Repeat("c", 64), CompletedAt: completedAt, LockedAt: completedAt,
		Assessor: fillereval.TemporalAssessorIdentity{
			ID: assessorID, Provider: "fixture", Model: family + "/model", ModelFamily: family,
			ModelDigest: strings.Repeat("d", 64), PromptVersion: "structure-fixture-v1",
		},
	}
	for _, truth := range challenge.Cases {
		decisive := []int64{100}
		switch truth.Unit {
		case fillereval.UnitCompilation:
			decisive = []int64{truth.JoinTimesMS[0]}
		case fillereval.UnitProgrammeExcerpt:
			decisive = []int64{100, durations[truth.Alias] - 100}
		}
		assessment := TemporalStructureAssessment{
			Alias:     truth.Alias,
			Unit:      &TemporalStructureUnitClaim{Kind: truth.Unit, DecisiveAtMS: decisive, Reason: "fixture unit"},
			Inference: temporalStructureTestInference(completedAt.Add(-time.Minute), false),
		}
		if truth.Unit == fillereval.UnitStandalone {
			assessment.Role = &TemporalStructureRoleClaim{Kind: truth.Role, DecisiveAtMS: []int64{100}, Reason: "fixture role"}
		}
		set.Assessments = append(set.Assessments, assessment)
	}
	return set
}

func differentTemporalStructureRole(role fillereval.TemporalRole) fillereval.TemporalRole {
	if role == fillereval.TemporalRoleCommercial {
		return fillereval.TemporalRolePromo
	}
	return fillereval.TemporalRoleCommercial
}
