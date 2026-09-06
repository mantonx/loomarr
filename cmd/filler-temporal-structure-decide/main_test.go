package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func TestRunPublishesTruthBlindStructureDecisionSummary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	args := []string{
		"--public", "manifest.json", "--authority-sha256", strings.Repeat("a", 64),
		"--assessment", "first.json", "--assessment", "second.json", "--cases", "60",
		"--decided-at", "2026-09-03T13:00:00Z", "--out", "decisions.json",
	}
	code := run(args, &stdout, &stderr, capabilities{publish: func(config fillerreview.TemporalStructureDecisionConfig) (fillerreview.TemporalStructureDecisionReport, string, error) {
		called = true
		if config.PublicManifestPath != "manifest.json" || config.PrivateAuthoritySHA256 != strings.Repeat("a", 64) || len(config.AssessmentPaths) != 2 || config.ExpectedCases != 60 || config.OutputPath != "decisions.json" {
			t.Fatalf("config = %+v", config)
		}
		return fillerreview.TemporalStructureDecisionReport{
			Cases: 60, ConfirmedCases: 41, HeldCases: 19, IndependentModelFamilies: 2,
		}, strings.Repeat("b", 64), nil
	}})
	if code != 0 || !called || stderr.Len() != 0 || !strings.Contains(stdout.String(), "41/60 cases confirmed") || !strings.Contains(stdout.String(), "19 held") || !strings.Contains(stdout.String(), "productionAdmissionAllowed=false") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunRequiresCompleteDecisionInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "at least two --assessment") {
		t.Fatalf("missing code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"--public", "manifest.json", "--authority-sha256", strings.Repeat("a", 64),
		"--assessment", "first.json", "--assessment", "second.json", "--cases", "60",
		"--decided-at", "now", "--out", "decisions.json",
	}
	if code := run(args, &stdout, &stderr, capabilities{}); code != 2 || !strings.Contains(stderr.String(), "--decided-at must be RFC3339") {
		t.Fatalf("time code=%d stderr=%q", code, stderr.String())
	}
}

func TestAssessmentPathsRejectsEmptyAndPreservesOrder(t *testing.T) {
	var paths assessmentPaths
	if err := paths.Set(""); err == nil {
		t.Fatal("empty assessment path was accepted")
	}
	if err := paths.Set("second.json"); err != nil {
		t.Fatal(err)
	}
	if err := paths.Set("first.json"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "second.json" || paths[1] != "first.json" {
		t.Fatalf("paths = %v", paths)
	}
}
