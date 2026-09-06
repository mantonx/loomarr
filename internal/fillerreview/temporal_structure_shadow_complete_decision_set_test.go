package fillerreview

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestPublishTemporalStructureCompleteShadowDecisionSetReplaysBlindedFamilies(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	firstPath := temporalStructureCompleteFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-a", time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondPath := temporalStructureCompleteFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-b", time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	output := filepath.Join(t.TempDir(), "complete-decisions.json")
	set, fileSHA, err := PublishTemporalStructureCompleteShadowDecisionSet(TemporalStructureCompleteShadowDecisionSetConfig{
		WindowSetManifestPath: config.WindowSetManifestPath, FirstFamilyPath: firstPath, SecondFamilyPath: secondPath,
		DecidedAt: time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC), OutputPath: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.InputKind != fillerstructure.AssessmentInputCompleteVideo || len(set.Cases) != TemporalStructureWindowCorpusCases ||
		len(set.Families) != 2 || set.Cases[0].Artifact.Decision.Status != fillerstructure.StatusConfirmed ||
		set.TrainingAllowed || set.ProductionAdmissionAllowed || !reviewSHA256(fileSHA) {
		t.Fatalf("set=%+v fileSHA=%q", set, fileSHA)
	}
	loaded, loadedFileSHA, err := LoadTemporalStructureShadowDecisionSet(output, config.WindowSetManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SHA256 != set.SHA256 || loadedFileSHA != fileSHA {
		t.Fatal("complete shadow decision set did not round trip")
	}
}

func temporalStructureCompleteFamilyArtifactFixture(t *testing.T, manifestPath, family string, completedAt time.Time) string {
	t.Helper()
	manifest := readStrictTestJSON[TemporalStructureWindowSetManifest](t, manifestPath)
	result, err := RunTemporalStructureCompleteFamily(t.Context(), TemporalStructureCompleteFamilyConfig{
		WindowSetManifestPath: manifestPath, ExpectedCases: TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Preparer:                 &fakeTemporalStructureCompletePreparer{root: t.TempDir(), profileSHA: manifest.AssessmentMediaProfileSHA256},
		Family:                   &fakeTemporalStructureCompleteFamily{profile: temporalStructureCompleteFamilyProfile(family)},
		Now:                      func() time.Time { return completedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), family+"-complete.json")
	if _, err := PublishTemporalStructureCompleteFamilyResult(path, manifestPath, result); err != nil {
		t.Fatal(err)
	}
	return path
}
