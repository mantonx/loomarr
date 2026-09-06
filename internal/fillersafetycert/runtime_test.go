package fillersafetycert

import (
	"os"
	"testing"
)

func TestLoadRuntimeAuthorityRequiresExactPassedCertification(t *testing.T) {
	fixture := newCertificationFixture(t)
	if _, _, err := Publish(Config{
		AuthorityPath: fixture.authorityPath, ResultsPath: fixture.resultsPath,
		ScoredAt: fixture.scoredAt, OutputPath: fixture.outputPath,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRuntimeAuthority(fixture.authorityPath, fixture.outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AuthoritySHA256() != fixture.manifest.AuthoritySHA256 ||
		loaded.Report().CertificationStatus != StatusPassed ||
		loaded.Authority().PolicySHA256 != loaded.Report().PolicySHA256 {
		t.Fatalf("loaded runtime authority = %+v", loaded)
	}

	mutated := loaded.Authority()
	mutated.Cases[0].Slices[0] = "mutated"
	reloaded, err := LoadRuntimeAuthority(fixture.authorityPath, fixture.outputPath)
	if err != nil || reloaded.Authority().Cases[0].Slices[0] == "mutated" {
		t.Fatal("runtime authority did not own a deep copy")
	}
}

func TestLoadRuntimeAuthorityRejectsAuthorityRewriteAndFailedReport(t *testing.T) {
	t.Run("authority bytes", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		if _, _, err := Publish(Config{
			AuthorityPath: fixture.authorityPath, ResultsPath: fixture.resultsPath,
			ScoredAt: fixture.scoredAt, OutputPath: fixture.outputPath,
		}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(fixture.authorityPath)
		if err != nil {
			t.Fatal(err)
		}
		raw = append([]byte(" \n"), raw...)
		if err := os.WriteFile(fixture.authorityPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRuntimeAuthority(fixture.authorityPath, fixture.outputPath); err == nil {
			t.Fatal("runtime accepted authority bytes different from the passing report")
		}
	})

	t.Run("failed certification", func(t *testing.T) {
		fixture := newCertificationFixture(t)
		report, _, err := Publish(Config{
			AuthorityPath: fixture.authorityPath, ResultsPath: fixture.resultsPath,
			ScoredAt: fixture.scoredAt, OutputPath: fixture.outputPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		report.DetectedPositiveSources--
		report.MissedPositiveSources++
		report.DetectedPositiveIntervals--
		report.SourceRecall = float64(report.DetectedPositiveSources) / float64(report.PositiveFamilies)
		report.SourceRecallExactLower95 = exactLower95(report.DetectedPositiveSources, report.PositiveFamilies)
		report.Cases[0].Outcome = OutcomeMissed
		report.Cases[0].DetectedPositiveIntervals = 0
		report.CertificationStatus = StatusFailed
		if err := validateReport(report); err != nil {
			t.Fatal(err)
		}
		writeFixtureJSON(t, fixture.outputPath, report)
		if _, err := LoadRuntimeAuthority(fixture.authorityPath, fixture.outputPath); err == nil {
			t.Fatal("runtime accepted a failed certification")
		}
	})
}
