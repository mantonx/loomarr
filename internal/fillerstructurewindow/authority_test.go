package fillerstructurewindow

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestVerifyMaterializationAuthorityReconstructsExactWindowProtocol(t *testing.T) {
	artifact := windowAuthorityArtifactFixture(t)
	authority := windowAuthorityFixture(artifact)
	if err := VerifyMaterializationAuthority(artifact, authority); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*MaterializationAuthority)
	}{
		{name: "permission", mutate: func(a *MaterializationAuthority) { a.AutomaticMaterializationAllowed = false }},
		{name: "window profile", mutate: func(a *MaterializationAuthority) { a.WindowProfileSHA256 = strings.Repeat("e", 64) }},
		{name: "source envelope", mutate: func(a *MaterializationAuthority) { a.MaximumSourceDurationMS-- }},
		{name: "window byte envelope", mutate: func(a *MaterializationAuthority) { a.MaximumWindowBytes-- }},
		{name: "window count envelope", mutate: func(a *MaterializationAuthority) { a.MaximumWindows-- }},
		{name: "assessor", mutate: func(a *MaterializationAuthority) { a.Assessors[0].Model = "different/model" }},
		{name: "unit", mutate: func(a *MaterializationAuthority) {
			a.AllowedUnits = []fillerstructure.Unit{fillerstructure.UnitProgrammeSpots}
		}},
		{name: "role", mutate: func(a *MaterializationAuthority) {
			a.AllowedRoles = []fillerstructure.Role{fillerstructure.RoleCommercial}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := authority
			candidate.Assessors = slices.Clone(authority.Assessors)
			candidate.AllowedUnits = slices.Clone(authority.AllowedUnits)
			candidate.AllowedRoles = slices.Clone(authority.AllowedRoles)
			test.mutate(&candidate)
			candidate.SHA256 = MaterializationAuthoritySHA256(candidate)
			if err := VerifyMaterializationAuthority(artifact, candidate); err == nil {
				t.Fatal("window decision escaped changed authority")
			}
		})
	}
}

func TestWindowMaterializationAuthorityCannotAuthorizeCompleteVideo(t *testing.T) {
	windowArtifact := windowAuthorityArtifactFixture(t)
	authority := windowAuthorityFixture(windowArtifact)
	request := fillerstructure.Request{
		Source: windowArtifact.Decision.Source, BoundaryToleranceMS: windowArtifact.BoundaryToleranceMS,
	}
	media := windowArtifact.Decision.Input.Items[0]
	media.DurationMS = request.Source.DurationMS
	media.LineageSHA256 = request.Source.SHA256
	input, err := fillerstructure.NewCompleteVideoInput(request.Source, media)
	if err != nil {
		t.Fatal(err)
	}
	request.Input = input
	request.Candidates = slices.Clone(windowArtifact.Decision.Candidates)
	for index := range request.Candidates {
		request.Candidates[index].InputSHA256 = input.SHA256
	}
	complete, err := fillerstructure.NewArtifact(request, time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMaterializationAuthority(complete, authority); err == nil {
		t.Fatal("window authority admitted a complete-video input")
	}
}

func windowAuthorityArtifactFixture(t *testing.T) fillerstructure.Artifact {
	t.Helper()
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	timeline := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	left, err := Stitch(set, assessmentsForFamily(t, set, timeline, "assessor-a", "family-a", "a"), 2_000)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Stitch(set, assessmentsForFamily(t, set, timeline, "assessor-b", "family-b", "b"), 2_000)
	if err != nil {
		t.Fatal(err)
	}
	input, first, err := ReducerCandidate(left)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := ReducerCandidate(right)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := fillerstructure.NewArtifact(fillerstructure.Request{
		Source: plan.Source, Input: input, BoundaryToleranceMS: 2_000,
		Candidates: []fillerstructure.Candidate{first, second},
	}, time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func windowAuthorityFixture(artifact fillerstructure.Artifact) MaterializationAuthority {
	authority := MaterializationAuthority{
		SchemaVersion: MaterializationAuthoritySchemaVersion, ContractVersion: MaterializationAuthorityContractVersion,
		WindowCertificationSHA256: strings.Repeat("c", 64), ShortLongShadowSHA256: strings.Repeat("d", 64),
		WindowProfileSHA256: CanonicalProfile().SHA256, AssessmentMediaProfileSHA256: artifact.Decision.Input.ProfileSHA256,
		MinimumSourceDurationMS: 1, MaximumSourceDurationMS: artifact.Decision.Source.DurationMS,
		MaximumWindowBytes: artifact.Decision.Input.Items[0].Bytes, MaximumWindows: len(artifact.Decision.Input.Items),
		ReducerVersion: artifact.ReducerVersion, BoundaryToleranceMS: artifact.BoundaryToleranceMS,
		AllowedUnits: []fillerstructure.Unit{fillerstructure.UnitCompilation},
		AllowedRoles: []fillerstructure.Role{fillerstructure.RoleCommercial, fillerstructure.RolePromo},
		ReviewerID:   "reviewer", ReviewedAt: time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC),
		AutomaticMaterializationAllowed: true,
	}
	for _, candidate := range artifact.Decision.Candidates {
		authority.Assessors = append(authority.Assessors, fillerstructure.Profile(candidate.Assessor))
	}
	authority.SHA256 = MaterializationAuthoritySHA256(authority)
	return authority
}
