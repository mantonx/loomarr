package fillerstructurewindowopenrouter

import (
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestValidateDeploymentBindsAuthorityFamiliesAndWorstCaseBudget(t *testing.T) {
	t.Parallel()
	authority := deploymentAuthorityFixture()
	deployment := deploymentFixture(authority)
	if err := ValidateDeployment(deployment, authority); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*Deployment){
		"authority":  func(value *Deployment) { value.AuthoritySHA256 = strings.Repeat("f", 64) },
		"permission": func(value *Deployment) { value.AutomaticAssessmentAllowed = false },
		"order": func(value *Deployment) {
			value.Families[0], value.Families[1] = value.Families[1], value.Families[0]
		},
		"model":         func(value *Deployment) { value.Families[0].Model = "other/model" },
		"reasoning":     func(value *Deployment) { value.Families[0].ReasoningMode = "optional" },
		"source budget": func(value *Deployment) { value.PerSourceBudgetNanoUSD-- },
		"day budget":    func(value *Deployment) { value.PerDayBudgetNanoUSD = value.PerSourceBudgetNanoUSD - 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := deployment
			changed.Families = append([]DeploymentFamily(nil), deployment.Families...)
			mutate(&changed)
			changed.SHA256 = DeploymentSHA256(changed)
			if err := ValidateDeployment(changed, authority); err == nil {
				t.Fatal("invalid deployment was accepted")
			}
		})
	}
}

func deploymentFixture(authority fillerstructurewindow.MaterializationAuthority) Deployment {
	result := Deployment{
		SchemaVersion: DeploymentSchemaVersion, ContractVersion: DeploymentContractVersion,
		AuthoritySHA256: authority.SHA256, PerSourceBudgetNanoUSD: 6_000,
		PerDayBudgetNanoUSD: 60_000, AutomaticAssessmentAllowed: true,
	}
	for _, profile := range authority.Assessors {
		result.Families = append(result.Families, DeploymentFamily{
			AssessorID: profile.ID, ModelFamily: profile.ModelFamily, Model: profile.Model,
			UpstreamProvider: "Provider " + profile.ID, UpstreamProviderSlug: "provider/" + profile.ID,
			ReasoningMode: ReasoningDisabled, MaximumInputTokens: 20_000, ReservationNanoUSD: 1_000,
		})
	}
	result.SHA256 = DeploymentSHA256(result)
	return result
}

func deploymentAuthorityFixture() fillerstructurewindow.MaterializationAuthority {
	profile := fillerstructurewindow.CanonicalProfile()
	authority := fillerstructurewindow.MaterializationAuthority{
		SchemaVersion:             fillerstructurewindow.MaterializationAuthoritySchemaVersion,
		ContractVersion:           fillerstructurewindow.MaterializationAuthorityContractVersion,
		WindowCertificationSHA256: strings.Repeat("a", 64), ShortLongShadowSHA256: strings.Repeat("b", 64),
		WindowProfileSHA256: profile.SHA256, AssessmentMediaProfileSHA256: profile.AssessmentMediaProfileSHA256,
		MinimumSourceDurationMS: 120_001, MaximumSourceDurationMS: 300_000,
		MaximumWindowBytes: 16 << 20, MaximumWindows: 3,
		ReducerVersion: fillerstructure.ReducerContractVersion, BoundaryToleranceMS: 2_000,
		AllowedUnits: []fillerstructure.Unit{fillerstructure.UnitCompilation},
		AllowedRoles: []fillerstructure.Role{fillerstructure.RoleCommercial},
		ReviewerID:   "maintainer", ReviewedAt: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
		AutomaticMaterializationAllowed: true,
	}
	for _, pair := range [][2]string{{"assessor-a", "family-a"}, {"assessor-b", "family-b"}} {
		authority.Assessors = append(authority.Assessors, fillerstructure.AssessorProfile{
			ID: pair[0], ModelFamily: pair[1], Provider: "openrouter", Model: "provider/model-" + pair[0],
			ModelDigest: strings.Repeat("c", 64), CapabilitySHA256: strings.Repeat("d", 64),
			PromptVersion:    fillerstructurewindow.DirectVideoPromptVersion,
			EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
		})
	}
	authority.SHA256 = fillerstructurewindow.MaterializationAuthoritySHA256(authority)
	return authority
}
