package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructurewindowopenrouter"
)

func TestLoadWindowStructureDeploymentRequiresExactAuthorityAndRegularFile(t *testing.T) {
	authority := appWindowStructureAuthorityFixture()
	deployment := appWindowStructureDeploymentFixture(authority.SHA256)
	raw, err := json.Marshal(deployment)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "deployment.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadWindowStructureDeployment(path, &authority)
	if err != nil || loaded == nil || loaded.SHA256 != deployment.SHA256 {
		t.Fatalf("loaded=%+v error=%v", loaded, err)
	}
	if _, err := loadWindowStructureDeployment(path, nil); err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("missing authority error=%v", err)
	}
	other := authority
	other.SHA256 = strings.Repeat("f", 64)
	if _, err := loadWindowStructureDeployment(path, &other); err == nil {
		t.Fatal("deployment accepted another authority")
	}
	link := filepath.Join(t.TempDir(), "deployment-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWindowStructureDeployment(link, &authority); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink error=%v", err)
	}
}

func appWindowStructureDeploymentFixture(authoritySHA string) fillerstructurewindowopenrouter.Deployment {
	result := fillerstructurewindowopenrouter.Deployment{
		SchemaVersion:   fillerstructurewindowopenrouter.DeploymentSchemaVersion,
		ContractVersion: fillerstructurewindowopenrouter.DeploymentContractVersion,
		AuthoritySHA256: authoritySHA, PerSourceBudgetNanoUSD: 6_000,
		PerDayBudgetNanoUSD: 60_000, AutomaticAssessmentAllowed: true,
	}
	for _, pair := range [][2]string{{"assessor-a", "family-a"}, {"assessor-b", "family-b"}} {
		result.Families = append(result.Families, fillerstructurewindowopenrouter.DeploymentFamily{
			AssessorID: pair[0], ModelFamily: pair[1], Model: "provider/model-" + pair[0],
			UpstreamProvider: "Provider " + pair[0], UpstreamProviderSlug: "provider/" + pair[0],
			ReasoningMode:      fillerstructurewindowopenrouter.ReasoningDisabled,
			MaximumInputTokens: 20_000, ReservationNanoUSD: 1_000,
		})
	}
	result.SHA256 = fillerstructurewindowopenrouter.DeploymentSHA256(result)
	return result
}
