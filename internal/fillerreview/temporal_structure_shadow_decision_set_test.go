package fillerreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestPublishTemporalStructureWindowShadowDecisionSetReplaysBlindedFamilies(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	firstPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-a", 5, time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-b", 6, time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	output := filepath.Join(t.TempDir(), "window-decisions.json")
	set, fileSHA, err := PublishTemporalStructureWindowShadowDecisionSet(TemporalStructureWindowShadowDecisionSetConfig{
		WindowSetManifestPath: config.WindowSetManifestPath, FirstFamilyPath: firstPath, SecondFamilyPath: secondPath,
		DecidedAt: time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC), OutputPath: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.InputKind != fillerstructure.AssessmentInputWindowMediaSet || len(set.Cases) != TemporalStructureWindowCorpusCases ||
		len(set.Families) != 2 || set.Cases[0].Artifact.Decision.Status != fillerstructure.StatusConfirmed ||
		set.TrainingAllowed || set.ProductionAdmissionAllowed || set.SHA256 != temporalStructureShadowDecisionSetSHA256(set) ||
		!reviewSHA256(fileSHA) {
		t.Fatalf("set=%+v fileSHA=%q", set, fileSHA)
	}
	loaded, loadedFileSHA, err := LoadTemporalStructureShadowDecisionSet(output, config.WindowSetManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SHA256 != set.SHA256 || loadedFileSHA != fileSHA {
		t.Fatalf("loaded set drifted: content=%q file=%q", loaded.SHA256, loadedFileSHA)
	}
	raw, err := os.ReadFile(output)
	if err != nil || hashBytes(raw) != fileSHA {
		t.Fatalf("published file drifted: %v", err)
	}
}

func TestPublishTemporalStructureWindowShadowDecisionSetRejectsTimeAndFamilyDrift(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	firstPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-a", 5, time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-b", 6, time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	base := TemporalStructureWindowShadowDecisionSetConfig{
		WindowSetManifestPath: config.WindowSetManifestPath, FirstFamilyPath: firstPath, SecondFamilyPath: secondPath,
		DecidedAt: time.Date(2026, 9, 13, 5, 30, 0, 0, time.UTC), OutputPath: filepath.Join(t.TempDir(), "early.json"),
	}
	if _, _, err := PublishTemporalStructureWindowShadowDecisionSet(base); err == nil || !strings.Contains(err.Error(), "predates") {
		t.Fatalf("early decision error=%v", err)
	}

	first := readStrictTestJSON[TemporalStructureWindowFamilyResult](t, firstPath)
	second := readStrictTestJSON[TemporalStructureWindowFamilyResult](t, secondPath)
	second.Assessor = first.Assessor
	second.SHA256 = temporalStructureWindowFamilySHA256(second)
	writeTestJSON(t, secondPath, second)
	base.DecidedAt = time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC)
	base.OutputPath = filepath.Join(t.TempDir(), "family-drift.json")
	if _, _, err := PublishTemporalStructureWindowShadowDecisionSet(base); err == nil {
		t.Fatal("window decision set accepted drifted family result")
	}
}

func TestValidateTemporalStructureShadowDecisionSetRejectsArtifactMutation(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	firstPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-a", 5, time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-b", 6, time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	set, _, err := PublishTemporalStructureWindowShadowDecisionSet(TemporalStructureWindowShadowDecisionSetConfig{
		WindowSetManifestPath: config.WindowSetManifestPath, FirstFamilyPath: firstPath, SecondFamilyPath: secondPath,
		DecidedAt: time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC), OutputPath: filepath.Join(t.TempDir(), "window-decisions.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	set.Cases[0].Artifact.Decision.Segments[0].Role = fillerstructure.RolePromo
	set.SHA256 = temporalStructureShadowDecisionSetSHA256(set)
	if err := ValidateTemporalStructureShadowDecisionSet(set); err == nil {
		t.Fatal("decision set accepted a mutated reducer artifact")
	}
}
