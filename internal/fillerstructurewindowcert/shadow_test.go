package fillerstructurewindowcert

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestCompareShortLongRequiresCompleteRepresentationAgreement(t *testing.T) {
	aliases, cases := shadowFixture(t)
	report, err := CompareShortLong(fixtureDigest(8000), fixtureDigest(8001), aliases, cases, shadowComparedAt())
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != ShadowStatusPassed || report.PassedCases != ShadowRequiredCases || report.FailedCases != 0 ||
		len(report.Cases) != ShadowRequiredCases || len(report.Profiles) != 2 || len(report.FailureCodes) != 0 ||
		report.TrainingAllowed || report.AutomaticMaterializationAllowed || report.SHA256 != ShadowReportSHA256(report) {
		t.Fatalf("report = %+v", report)
	}
	if err := ValidateShadowReport(report); err != nil {
		t.Fatal(err)
	}
}

func TestCompareShortLongFailsHeldAndSemanticDisagreement(t *testing.T) {
	aliases, cases := shadowFixture(t)
	shifted := slices.Clone(certificationTruth())
	shifted[0].EndMS += BoundaryToleranceMS + 1
	shifted[1].StartMS = shifted[0].EndMS
	short, long := shadowArtifacts(t, 0, certificationTruth(), shifted)
	cases[0].CompleteVideo, cases[0].WindowMediaSet = short, long

	report, err := CompareShortLong(fixtureDigest(8100), fixtureDigest(8101), aliases, cases, shadowComparedAt())
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != ShadowStatusFailed || report.FailedCases != 1 || report.PassedCases != ShadowRequiredCases-1 ||
		!slices.Contains(report.FailureCodes, "boundary_disagreement") || report.AutomaticMaterializationAllowed {
		t.Fatalf("report = %+v", report)
	}

	aliases, cases = shadowFixture(t)
	cases[0].WindowMediaSet = shadowHeldWindowArtifact(t, 0)
	report, err = CompareShortLong(fixtureDigest(8110), fixtureDigest(8111), aliases, cases, shadowComparedAt())
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != ShadowStatusFailed || !slices.Contains(report.FailureCodes, "window_media_set_held") {
		t.Fatalf("held report = %+v", report)
	}
}

func TestCompareShortLongRejectsMissingWrongKindAndModelDrift(t *testing.T) {
	aliases, cases := shadowFixture(t)
	report, err := CompareShortLong(fixtureDigest(8200), fixtureDigest(8201), aliases, cases[1:], shadowComparedAt())
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != ShadowStatusFailed || report.FailedCases != 1 || !slices.Contains(report.FailureCodes, "missing_case") {
		t.Fatalf("missing report = %+v", report)
	}

	aliases, cases = shadowFixture(t)
	cases[0].CompleteVideo = cases[0].WindowMediaSet
	if _, err := CompareShortLong(fixtureDigest(8210), fixtureDigest(8211), aliases, cases, shadowComparedAt()); err == nil {
		t.Fatal("shadow accepted two window representations")
	}

	aliases, cases = shadowFixture(t)
	drifted := certificationTruth()
	short, long := shadowArtifactsWithLongModel(t, 0, drifted, "other/model")
	cases[0].CompleteVideo, cases[0].WindowMediaSet = short, long
	if _, err := CompareShortLong(fixtureDigest(8220), fixtureDigest(8221), aliases, cases, shadowComparedAt()); err == nil {
		t.Fatal("shadow accepted model drift across representations")
	}
}

func TestValidateShadowReportRejectsDetachedMutation(t *testing.T) {
	aliases, cases := shadowFixture(t)
	report, err := CompareShortLong(fixtureDigest(8300), fixtureDigest(8301), aliases, cases, shadowComparedAt())
	if err != nil {
		t.Fatal(err)
	}
	report.Cases[0].CompleteVideo.Decision.Segments[0].Role = fillerstructure.RolePromo
	report.SHA256 = ShadowReportSHA256(report)
	if err := ValidateShadowReport(report); err == nil {
		t.Fatal("shadow report accepted a detached artifact mutation")
	}
}

func shadowFixture(t *testing.T) ([]string, []ShadowCase) {
	t.Helper()
	aliases := make([]string, ShadowRequiredCases)
	cases := make([]ShadowCase, ShadowRequiredCases)
	for index := range ShadowRequiredCases {
		alias := fmt.Sprintf("case-%02d", index)
		short, long := shadowArtifacts(t, index, certificationTruth(), certificationTruth())
		aliases[index] = alias
		cases[index] = ShadowCase{Alias: alias, CompleteVideo: short, WindowMediaSet: long}
	}
	return aliases, cases
}

func shadowArtifacts(t *testing.T, seed int, shortTimeline, longTimeline []fillerstructure.Segment) (fillerstructure.Artifact, fillerstructure.Artifact) {
	t.Helper()
	return shadowArtifactsWithModels(t, seed, shortTimeline, longTimeline, "family-a/model", "family-a/model")
}

func shadowArtifactsWithLongModel(t *testing.T, seed int, timeline []fillerstructure.Segment, firstModel string) (fillerstructure.Artifact, fillerstructure.Artifact) {
	t.Helper()
	return shadowArtifactsWithModels(t, seed, timeline, timeline, "family-a/model", firstModel)
}

func shadowArtifactsWithModels(t *testing.T, seed int, shortTimeline, longTimeline []fillerstructure.Segment, shortFirstModel, longFirstModel string) (fillerstructure.Artifact, fillerstructure.Artifact) {
	t.Helper()
	set := certificationMediaSet(t, seed)
	windowProfiles := []fillerstructure.AssessorProfile{
		assessorProfile("assessor-a-window", "family-a"),
		assessorProfile("assessor-b-window", "family-b"),
	}
	windowProfiles[0].Model = longFirstModel
	shortProfiles := slices.Clone(windowProfiles)
	shortProfiles[0].Model = shortFirstModel
	for index := range shortProfiles {
		shortProfiles[index].ID = fmt.Sprintf("assessor-%d-complete", index)
		shortProfiles[index].PromptVersion = "complete-video-v1"
		shortProfiles[index].EvidenceContract = "complete-video-evidence-v1"
	}
	media := fillerstructure.AssessmentMedia{
		SHA256: fixtureDigest(9000 + seed), Bytes: 32 << 20, DurationMS: set.Plan.Source.DurationMS,
		ProfileSHA256: set.Plan.Profile.AssessmentMediaProfileSHA256, LineageSHA256: fixtureDigest(9100 + seed),
	}
	input, err := fillerstructure.NewCompleteVideoInput(set.Plan.Source, media)
	if err != nil {
		t.Fatal(err)
	}
	shortCandidates := make([]fillerstructure.Candidate, 2)
	for index, profile := range shortProfiles {
		shortCandidates[index] = fillerstructure.Candidate{
			Source: set.Plan.Source, InputSHA256: input.SHA256,
			Assessor: shadowAssessor(profile, fixtureDigest(9200+seed*10+index)),
			Unit:     fillerstructure.UnitProgrammeSpots, Segments: slices.Clone(shortTimeline),
		}
	}
	shortArtifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: set.Plan.Source, Input: input, BoundaryToleranceMS: BoundaryToleranceMS, Candidates: shortCandidates,
	}, time.Date(2026, 9, 8, 10, 0, seed, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	windowInput, firstCandidate, err := fillerstructurewindow.ReducerCandidate(stitchForTimeline(t, set, windowProfiles[0], longTimeline))
	if err != nil {
		t.Fatal(err)
	}
	_, secondCandidate, err := fillerstructurewindow.ReducerCandidate(stitchForTimeline(t, set, windowProfiles[1], longTimeline))
	if err != nil {
		t.Fatal(err)
	}
	longArtifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: set.Plan.Source, Input: windowInput, BoundaryToleranceMS: BoundaryToleranceMS,
		Candidates: []fillerstructure.Candidate{firstCandidate, secondCandidate},
	}, time.Date(2026, 9, 8, 11, 0, seed, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return shortArtifact, longArtifact
}

func shadowHeldWindowArtifact(t *testing.T, seed int) fillerstructure.Artifact {
	t.Helper()
	set := certificationMediaSet(t, seed)
	profiles := []fillerstructure.AssessorProfile{
		assessorProfile("assessor-a-window", "family-a"),
		assessorProfile("assessor-b-window", "family-b"),
	}
	input, candidate, err := fillerstructurewindow.ReducerCandidate(stitchForTimeline(t, set, profiles[0], certificationTruth()))
	if err != nil {
		t.Fatal(err)
	}
	failed := candidate
	failed.Assessor = shadowAssessor(profiles[1], fixtureDigest(9900+seed))
	failed.Failure = "provider"
	failed.Unit, failed.Role, failed.Segments = "", "", nil
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: set.Plan.Source, Input: input, BoundaryToleranceMS: BoundaryToleranceMS,
		Candidates: []fillerstructure.Candidate{candidate, failed},
	}, time.Date(2026, 9, 8, 12, 0, seed, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func shadowAssessor(profile fillerstructure.AssessorProfile, assessmentSHA string) fillerstructure.Assessor {
	return fillerstructure.Assessor{
		ID: profile.ID, ModelFamily: profile.ModelFamily, Provider: profile.Provider, Model: profile.Model,
		ModelDigest: profile.ModelDigest, CapabilitySHA256: profile.CapabilitySHA256,
		PromptVersion: profile.PromptVersion, EvidenceContract: profile.EvidenceContract,
		AssessmentSHA256: assessmentSHA,
	}
}

func shadowComparedAt() time.Time { return time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC) }
