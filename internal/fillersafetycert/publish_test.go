package fillersafetycert

import (
	"os"
	"testing"
)

func TestPublishCertifiesExhaustiveSourceDisjointCascade(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)

	report, digest, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != StatusPassed || report.DetectedPositiveSources != MinimumPositiveFamilies ||
		report.PositiveFamilies != MinimumPositiveFamilies || report.MissedPositiveSources != 0 ||
		report.SourceRecallExactLower95 < 0.95 || report.CleanSources != len(requiredCleanSlices()) ||
		report.CleanFalsePositiveSources != 0 || report.CoverageHolds != 0 || !validSHA256(digest) ||
		report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed {
		t.Fatalf("report=%+v digest=%q", report, digest)
	}
	info, err := os.Stat(fixture.outputPath)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("output mode=%v err=%v", info.Mode(), err)
	}
	raw, err := os.ReadFile(fixture.outputPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitiveVocabulary(t, string(raw))
}

func TestDevelopmentAuthorityCanOnlyPassDiagnostically(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	fixture.authority.ChallengeKind = ChallengeDevelopment
	fixture.rewriteAuthority(t)

	report, _, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != StatusDiagnosticPassed {
		t.Fatalf("status=%s", report.CertificationStatus)
	}
}

func TestPublishDoesNotOverwriteAReport(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	if err := os.WriteFile(fixture.outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Publish(fixture.config()); err == nil {
		t.Fatal("expected existing output to be preserved")
	}
	raw, err := os.ReadFile(fixture.outputPath)
	if err != nil || string(raw) != "existing" {
		t.Fatalf("output=%q err=%v", raw, err)
	}
}
