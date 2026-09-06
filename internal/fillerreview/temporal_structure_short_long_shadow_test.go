package fillerreview

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func TestPublishTemporalStructureShortLongShadowBindsCertifiedFamilyLineage(t *testing.T) {
	fixture := temporalStructureShortLongShadowFixture(t)
	artifact, fileSHA, err := PublishTemporalStructureShortLongShadow(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Report.Status != fillerstructurewindowcert.ShadowStatusPassed ||
		artifact.Report.PassedCases != TemporalStructureWindowCorpusCases || artifact.Report.FailedCases != 0 ||
		artifact.TrainingAllowed || artifact.ProductionAdmissionAllowed || artifact.AutomaticMaterializationAllowed ||
		artifact.SHA256 != temporalStructureShortLongShadowSHA256(artifact) || !reviewSHA256(fileSHA) {
		t.Fatalf("artifact=%+v fileSHA=%q", artifact, fileSHA)
	}
	if err := ValidateTemporalStructureShortLongShadowArtifact(artifact); err != nil {
		t.Fatal(err)
	}
}

func TestPublishTemporalStructureShortLongShadowRejectsUncertifiedWindowLineage(t *testing.T) {
	fixture := temporalStructureShortLongShadowFixture(t)
	windowSet := readStrictTestJSON[TemporalStructureShadowDecisionSet](t, fixture.config.WindowDecisionSetPath)
	windowSet.Families[0].ResultFileSHA256 = strings.Repeat("f", 64)
	windowSet.SHA256 = temporalStructureShadowDecisionSetSHA256(windowSet)
	writeTestJSON(t, fixture.config.WindowDecisionSetPath, windowSet)
	fixture.config.OutputPath = filepath.Join(t.TempDir(), "lineage-drift.json")
	if _, _, err := PublishTemporalStructureShortLongShadow(fixture.config); err == nil || !strings.Contains(err.Error(), "does not descend") {
		t.Fatalf("lineage error=%v", err)
	}
}

func TestPublishTemporalStructureShortLongShadowRejectsFailedCertificate(t *testing.T) {
	fixture := temporalStructureShortLongShadowFixture(t)
	certificate := readStrictTestJSON[TemporalStructureWindowCertificationArtifact](t, fixture.config.WindowCertificationPath)
	certificate.Report.Status = fillerstructurewindowcert.StatusFailed
	certificate.Report.NextAction = "diagnose_long_reel_certification_failures"
	certificate.Report.SHA256 = fillerstructurewindowcert.ReportSHA256(certificate.Report)
	certificate.SHA256 = temporalStructureWindowCertificationSHA256(certificate)
	writeTestJSON(t, fixture.config.WindowCertificationPath, certificate)
	fixture.config.OutputPath = filepath.Join(t.TempDir(), "failed-certificate.json")
	if _, _, err := PublishTemporalStructureShortLongShadow(fixture.config); err == nil || !strings.Contains(err.Error(), "completely passing") {
		t.Fatalf("failed certificate error=%v", err)
	}
}

type temporalStructureShortLongShadowTestFixture struct {
	config TemporalStructureShortLongShadowConfig
}

func temporalStructureShortLongShadowFixture(t *testing.T) temporalStructureShortLongShadowTestFixture {
	t.Helper()
	root := t.TempDir()
	suiteConfig, _ := temporalStructureWindowSuiteFixture(t, filepath.Join(root, "suite"))
	if _, err := BuildTemporalStructureWindowCertificationSuite(t.Context(), suiteConfig); err != nil {
		t.Fatal(err)
	}
	firstWindow := temporalStructureWindowFamilyArtifactFixture(t, suiteConfig.WindowSetManifestPath, "family-a", 5, time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondWindow := temporalStructureWindowFamilyArtifactFixture(t, suiteConfig.WindowSetManifestPath, "family-b", 6, time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	certificatePath := filepath.Join(root, "window-certificate.json")
	if _, _, err := PublishTemporalStructureWindowCertification(TemporalStructureWindowCertificationConfig{
		SuitePath: filepath.Join(suiteConfig.OutputDir, "private", "suite.json"), WindowSetManifestPath: suiteConfig.WindowSetManifestPath,
		FirstFamilyPath: firstWindow, SecondFamilyPath: secondWindow,
		CertifiedAt: time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC), OutputPath: certificatePath,
	}); err != nil {
		t.Fatal(err)
	}
	// This publisher boundary consumes an already-certified artifact. The private truth scorer has
	// its own tests; make this fixture's otherwise synthetic all-commercial families a passing input.
	passing := readStrictTestJSON[TemporalStructureWindowCertificationArtifact](t, certificatePath)
	passing.Report.Status = fillerstructurewindowcert.StatusPassed
	passing.Report.DecidedCases = passing.Report.Cases
	passing.Report.HeldCases = 0
	passing.Report.WrongCases = 0
	passing.Report.FailureCodes = nil
	passing.Report.NextAction = "run_locked_short_long_shadow_comparison"
	for index := range passing.Report.Slices {
		passing.Report.Slices[index].DecidedCases = passing.Report.Slices[index].Cases
		passing.Report.Slices[index].HeldCases = 0
		passing.Report.Slices[index].WrongCases = 0
		passing.Report.Slices[index].FailureCodes = nil
		passing.Report.Slices[index].Passed = true
	}
	passing.Report.SHA256 = fillerstructurewindowcert.ReportSHA256(passing.Report)
	passing.SHA256 = temporalStructureWindowCertificationSHA256(passing)
	writeTestJSON(t, certificatePath, passing)
	windowDecisions := filepath.Join(root, "window-decisions.json")
	if _, _, err := PublishTemporalStructureWindowShadowDecisionSet(TemporalStructureWindowShadowDecisionSetConfig{
		WindowSetManifestPath: suiteConfig.WindowSetManifestPath, FirstFamilyPath: firstWindow, SecondFamilyPath: secondWindow,
		DecidedAt: time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC), OutputPath: windowDecisions,
	}); err != nil {
		t.Fatal(err)
	}
	firstComplete := temporalStructureCompleteFamilyArtifactFixture(t, suiteConfig.WindowSetManifestPath, "family-a", time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC))
	secondComplete := temporalStructureCompleteFamilyArtifactFixture(t, suiteConfig.WindowSetManifestPath, "family-b", time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC))
	completeDecisions := filepath.Join(root, "complete-decisions.json")
	if _, _, err := PublishTemporalStructureCompleteShadowDecisionSet(TemporalStructureCompleteShadowDecisionSetConfig{
		WindowSetManifestPath: suiteConfig.WindowSetManifestPath, FirstFamilyPath: firstComplete, SecondFamilyPath: secondComplete,
		DecidedAt: time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC), OutputPath: completeDecisions,
	}); err != nil {
		t.Fatal(err)
	}
	return temporalStructureShortLongShadowTestFixture{config: TemporalStructureShortLongShadowConfig{
		WindowSetManifestPath: suiteConfig.WindowSetManifestPath, WindowCertificationPath: certificatePath,
		CompleteDecisionSetPath: completeDecisions, WindowDecisionSetPath: windowDecisions,
		ComparedAt: time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC), OutputPath: filepath.Join(root, "shadow.json"),
	}}
}
