package filler

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestProjectConfirmedStructureDecisionUsesOnlyArtifactBoundaries(t *testing.T) {
	source := structureSource(60_000)
	detector, err := AssessSourceStructure(SourceStructureInput{
		Source: source,
		Observations: []StructureObservation{
			structureObservation("detector-black", ObservationBlackInterval, ObservationProposesBoundary, 27_900, 28_100),
			structureObservation("detector-silence", ObservationSilenceInterval, ObservationProposesBoundary, 27_900, 28_100),
		},
		AssessedAt: time.Date(2026, time.September, 10, 1, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := projectedStructureArtifact(t, source, fillerstructure.UnitCompilation,
		[]fillerstructure.Segment{
			{StartMS: 0, EndMS: 29_000, Role: fillerstructure.RoleCommercial},
			{StartMS: 29_000, EndMS: 60_000, Role: fillerstructure.RolePromo},
		},
		[]fillerstructure.Segment{
			{StartMS: 0, EndMS: 31_000, Role: fillerstructure.RoleCommercial},
			{StartMS: 31_000, EndMS: 60_000, Role: fillerstructure.RolePromo},
		})

	assessment, err := ProjectConfirmedStructureDecision(source, &detector, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Kind != StructureCompilationBreak || len(assessment.Boundaries) != 1 || assessment.Boundaries[0].AtMs != 30_000 ||
		len(assessment.Plan) != 2 || assessment.Plan[0].EndMs != 30_000 || assessment.Plan[1].StartMs != 30_000 {
		t.Fatalf("projected assessment = %+v", assessment)
	}
	for _, observation := range assessment.Observations {
		if strings.HasPrefix(observation.ID, "detector-") && observation.Effect != ObservationContextOnly {
			t.Fatalf("detector observation retained a boundary vote: %+v", observation)
		}
	}
	if err := ValidateStructureDecisionProjection(assessment, artifact); err != nil {
		t.Fatal(err)
	}

	replayed, err := ProjectConfirmedStructureDecision(source, &assessment, artifact)
	if err != nil || replayed.SHA256 != assessment.SHA256 {
		t.Fatalf("replayed projection digest=%s want=%s error=%v", replayed.SHA256, assessment.SHA256, err)
	}
}

func TestProjectConfirmedStructureDecisionKeepsProgrammeOutOfFillerPlan(t *testing.T) {
	source := structureSource(70_000)
	segments := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 20_000, Role: fillerstructure.RoleProgrammeFragment},
		{StartMS: 20_000, EndMS: 50_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 50_000, EndMS: 70_000, Role: fillerstructure.RoleProgrammeFragment},
	}
	artifact := projectedStructureArtifact(t, source, fillerstructure.UnitProgrammeSpots, segments, segments)
	assessment, err := ProjectConfirmedStructureDecision(source, nil, artifact)
	if err != nil {
		t.Fatal(err)
	}
	want := []StructureSegmentDisposition{StructureDiscard, StructureKeep, StructureDiscard}
	got := []StructureSegmentDisposition{
		assessment.Plan[0].Disposition, assessment.Plan[1].Disposition, assessment.Plan[2].Disposition,
	}
	if assessment.Kind != StructureProgrammeSpots || !slices.Equal(got, want) ||
		assessment.Plan[0].DiscardReason != DiscardProgrammeMaterial || assessment.Plan[2].DiscardReason != DiscardProgrammeMaterial {
		t.Fatalf("programme projection = %+v", assessment)
	}
}

func TestProjectConfirmedStructureDecisionKeepsInterstitialRole(t *testing.T) {
	source := structureSource(60_000)
	segments := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 30_000, Role: fillerstructure.RoleInterstitial},
		{StartMS: 30_000, EndMS: 60_000, Role: fillerstructure.RoleCommercial},
	}
	artifact := projectedStructureArtifact(t, source, fillerstructure.UnitCompilation, segments, segments)
	assessment, err := ProjectConfirmedStructureDecision(source, nil, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Kind != StructureCompilationBreak || assessment.Plan[0].Disposition != StructureKeep ||
		assessment.Plan[0].Role != SegmentRoleInterstitial {
		t.Fatalf("interstitial projection = %+v", assessment)
	}
}

func TestProjectConfirmedStructureDecisionRejectsHeldOrDriftedEvidence(t *testing.T) {
	source := structureSource(60_000)
	first := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 30_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 30_000, EndMS: 60_000, Role: fillerstructure.RolePromo},
	}
	second := slices.Clone(first)
	second[1].Role = fillerstructure.RolePSA
	held := projectedStructureArtifact(t, source, fillerstructure.UnitCompilation, first, second)
	if _, err := ProjectConfirmedStructureDecision(source, nil, held); err == nil {
		t.Fatal("held artifact projected")
	}

	confirmed := projectedStructureArtifact(t, source, fillerstructure.UnitCompilation, first, first)
	drifted := source
	drifted.SHA256 = strings.Repeat("f", 64)
	if _, err := ProjectConfirmedStructureDecision(drifted, nil, confirmed); err == nil {
		t.Fatal("source-drifted artifact projected")
	}

	detector, err := ProjectConfirmedStructureDecision(source, nil, confirmed)
	if err != nil {
		t.Fatal(err)
	}
	detector.SHA256 = strings.Repeat("0", 64)
	if _, err := ProjectConfirmedStructureDecision(source, &detector, confirmed); err == nil {
		t.Fatal("invalid detector context projected")
	}
}

func projectedStructureArtifact(t *testing.T, source SplitSourceAsset, unit fillerstructure.Unit, first, second []fillerstructure.Segment) fillerstructure.Artifact {
	t.Helper()
	coreSource := fillerstructure.Source{SHA256: source.SHA256, Bytes: source.Bytes, DurationMS: source.DurationMs}
	media := fillerstructure.AssessmentMedia{SHA256: strings.Repeat("9", 64), Bytes: source.Bytes, DurationMS: source.DurationMs, ProfileSHA256: strings.Repeat("8", 64), LineageSHA256: strings.Repeat("7", 64)}
	input, err := fillerstructure.NewCompleteVideoInput(coreSource, media)
	if err != nil {
		t.Fatal(err)
	}
	candidate := func(id, family, assessmentDigest string, segments []fillerstructure.Segment) fillerstructure.Candidate {
		return fillerstructure.Candidate{
			Source: coreSource, InputSHA256: input.SHA256,
			Assessor: fillerstructure.Assessor{
				ID: id, ModelFamily: family, Provider: "fixture-provider", Model: "fixture-model",
				ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
				PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1",
				AssessmentSHA256: strings.Repeat(assessmentDigest, 64),
			},
			Unit: unit, Segments: slices.Clone(segments),
		}
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: coreSource, Input: input, BoundaryToleranceMS: 2_000,
		Candidates: []fillerstructure.Candidate{
			candidate("assessor-a", "family-a", "1", first),
			candidate("assessor-b", "family-b", "2", second),
		},
	}, time.Date(2026, time.September, 10, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}
