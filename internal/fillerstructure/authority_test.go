package fillerstructure

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestVerifyAuthorityRequiresExactCertifiedProfilesAndSlices(t *testing.T) {
	artifact, err := NewArtifact(fixtureRequest(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	authority := fixtureAuthority(artifact)
	if err := VerifyAuthority(artifact, authority); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Authority)
	}{
		{name: "permission", mutate: func(a *Authority) { a.AutomaticMaterializationAllowed = false }},
		{name: "model", mutate: func(a *Authority) { a.Assessors[0].Model = "another-model" }},
		{name: "media profile", mutate: func(a *Authority) { a.AssessmentMediaProfileSHA256 = strings.Repeat("e", 64) }},
		{name: "duration envelope", mutate: func(a *Authority) { a.MaximumSourceDurationMS-- }},
		{name: "byte envelope", mutate: func(a *Authority) { a.MaximumAssessmentMediaBytes-- }},
		{name: "unit slice", mutate: func(a *Authority) { a.AllowedUnits = []Unit{UnitProgrammeSpots} }},
		{name: "role slice", mutate: func(a *Authority) { a.AllowedRoles = []Role{RoleCommercial} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := authority
			candidate.Assessors = slices.Clone(authority.Assessors)
			candidate.AllowedUnits = slices.Clone(authority.AllowedUnits)
			candidate.AllowedRoles = slices.Clone(authority.AllowedRoles)
			test.mutate(&candidate)
			candidate.SHA256 = AuthoritySHA256(candidate)
			if err := VerifyAuthority(artifact, candidate); err == nil {
				t.Fatal("decision escaped changed authority")
			}
		})
	}
}

func TestVerifyAuthorityRejectsHeldDecision(t *testing.T) {
	passing, err := NewArtifact(fixtureRequest(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := fixtureRequest()
	request.Candidates[1].Segments[0].Role = RolePSA
	held, err := NewArtifact(request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuthority(held, fixtureAuthority(passing)); err == nil {
		t.Fatal("held decision was authorized")
	}
}

func TestCompleteVideoAuthorityCannotAuthorizeWindowMediaSet(t *testing.T) {
	request := fixtureRequest()
	windowed, err := NewWindowMediaSetInput(request.Source, strings.Repeat("9", 64), request.Input.Items)
	if err != nil {
		t.Fatal(err)
	}
	request.Input = windowed
	for index := range request.Candidates {
		request.Candidates[index].InputSHA256 = windowed.SHA256
	}
	artifact, err := NewArtifact(request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	passing, err := NewArtifact(fixtureRequest(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuthority(artifact, fixtureAuthority(passing)); err == nil {
		t.Fatal("complete-video authority admitted a window media set")
	}
}

func fixtureAuthority(artifact Artifact) Authority {
	authority := Authority{
		SchemaVersion: AuthoritySchemaVersion, ContractVersion: AuthorityContractVersion,
		CertificateSHA256: strings.Repeat("f", 64), AssessmentMediaProfileSHA256: artifact.Decision.Input.ProfileSHA256,
		MinimumSourceDurationMS: 1, MaximumSourceDurationMS: artifact.Decision.Source.DurationMS,
		MaximumAssessmentMediaBytes: artifact.Decision.Input.Items[0].Bytes, ReducerVersion: ReducerContractVersion,
		BoundaryToleranceMS: artifact.BoundaryToleranceMS, AllowedUnits: []Unit{UnitCompilation},
		AllowedRoles: []Role{RoleCommercial, RolePromo}, AutomaticMaterializationAllowed: true,
	}
	for _, candidate := range artifact.Decision.Candidates {
		authority.Assessors = append(authority.Assessors, Profile(candidate.Assessor))
	}
	authority.SHA256 = AuthoritySHA256(authority)
	return authority
}
