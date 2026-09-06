package fillerreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func TestPublishTemporalStructureWindowCertificationJoinsTwoBlindedFamiliesToPrivateSuite(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	if _, err := BuildTemporalStructureWindowCertificationSuite(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	firstPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-a", 5, time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-b", 6, time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	output := filepath.Join(t.TempDir(), "certification.json")
	artifact, digest, err := PublishTemporalStructureWindowCertification(TemporalStructureWindowCertificationConfig{
		SuitePath: filepath.Join(config.OutputDir, "private", "suite.json"), WindowSetManifestPath: config.WindowSetManifestPath,
		FirstFamilyPath: firstPath, SecondFamilyPath: secondPath,
		CertifiedAt: time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC), OutputPath: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	report := artifact.Report
	if report.Cases != TemporalStructureWindowCorpusCases || len(report.AssessorProfiles) != 2 ||
		report.AssessorProfiles[0].ModelFamily != "family-a" || report.AssessorProfiles[1].ModelFamily != "family-b" ||
		report.TrainingAllowed || report.AutomaticMaterializationAllowed ||
		report.SHA256 != fillerstructurewindowcert.ReportSHA256(report) ||
		artifact.SHA256 != temporalStructureWindowCertificationSHA256(artifact) || !reviewSHA256(digest) {
		t.Fatalf("artifact=%+v digest=%q", artifact, digest)
	}
	raw, err := os.ReadFile(output)
	if err != nil || hashBytes(raw) != digest {
		t.Fatalf("published digest drifted: %v", err)
	}
}

func TestPublishTemporalStructureWindowCertificationRejectsFamilyOrTimeDrift(t *testing.T) {
	config, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	if _, err := BuildTemporalStructureWindowCertificationSuite(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	firstPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-a", 5, time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondPath := temporalStructureWindowFamilyArtifactFixture(t, config.WindowSetManifestPath, "family-b", 6, time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	base := TemporalStructureWindowCertificationConfig{
		SuitePath: filepath.Join(config.OutputDir, "private", "suite.json"), WindowSetManifestPath: config.WindowSetManifestPath,
		FirstFamilyPath: firstPath, SecondFamilyPath: secondPath,
		CertifiedAt: time.Date(2026, 9, 13, 5, 30, 0, 0, time.UTC), OutputPath: filepath.Join(t.TempDir(), "early.json"),
	}
	if _, _, err := PublishTemporalStructureWindowCertification(base); err == nil || !strings.Contains(err.Error(), "predates") {
		t.Fatalf("early certification error=%v", err)
	}

	drifted := readStrictTestJSON[TemporalStructureWindowFamilyResult](t, secondPath)
	drifted.WindowSetManifestSHA256 = strings.Repeat("f", 64)
	drifted.SHA256 = temporalStructureWindowFamilySHA256(drifted)
	writeTestJSON(t, secondPath, drifted)
	base.CertifiedAt = time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC)
	base.OutputPath = filepath.Join(t.TempDir(), "mismatch.json")
	if _, _, err := PublishTemporalStructureWindowCertification(base); err == nil {
		t.Fatal("expected mismatched public-manifest family rejection")
	}
}

func temporalStructureWindowFamilyArtifactFixture(t *testing.T, manifestPath, family string, hour int, completedAt time.Time) string {
	t.Helper()
	result, err := RunTemporalStructureWindowFamily(t.Context(), TemporalStructureWindowFamilyConfig{
		WindowSetManifestPath: manifestPath, ExpectedCases: TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: temporalStructureFamilySnapshotSHA256,
		Family:                   &fakeTemporalStructureWindowFamily{profile: temporalStructureWindowFamilyProfile(family)},
		Now:                      func() time.Time { return completedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), family+"-"+time.Date(2026, 9, 13, hour, 0, 0, 0, time.UTC).Format("1504")+".json")
	if _, err := PublishTemporalStructureWindowFamilyResult(path, manifestPath, result); err != nil {
		t.Fatal(err)
	}
	return path
}
