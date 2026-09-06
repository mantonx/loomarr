package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func TestRunPublishesWindowCertificationSummary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	args := []string{
		"--suite", "suite.json", "--window-set", "manifest.json",
		"--first-family", "first.json", "--second-family", "second.json",
		"--certified-at", "2026-09-13T07:00:00Z", "--out", "certification.json",
	}
	code := run(args, &stdout, &stderr, capabilities{publish: func(config fillerreview.TemporalStructureWindowCertificationConfig) (fillerreview.TemporalStructureWindowCertificationArtifact, string, error) {
		called = true
		if config.SuitePath != "suite.json" || config.WindowSetManifestPath != "manifest.json" ||
			config.FirstFamilyPath != "first.json" || config.SecondFamilyPath != "second.json" || config.OutputPath != "certification.json" {
			t.Fatalf("config=%+v", config)
		}
		return fillerreview.TemporalStructureWindowCertificationArtifact{
			Families: []fillerreview.TemporalStructureWindowCertificationFamilyEvidence{{}, {}},
			Report:   fillerstructurewindowcert.Report{Status: fillerstructurewindowcert.StatusFailed, Cases: 28, DecidedCases: 24, WrongCases: 2, HeldCases: 4},
		}, strings.Repeat("a", 64), nil
	}})
	if code != 0 || !called || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "failed; 24/28 decided, 2 wrong, 4 held across 2 model families") ||
		!strings.Contains(stdout.String(), "training=false automaticMaterialization=false") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunRequiresCompleteWindowCertificationInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "two family results") {
		t.Fatalf("missing code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"--suite", "suite.json", "--window-set", "manifest.json",
		"--first-family", "first.json", "--second-family", "second.json",
		"--certified-at", "now", "--out", "certification.json",
	}
	if code := run(args, &stdout, &stderr, capabilities{}); code != 2 || !strings.Contains(stderr.String(), "--certified-at must be RFC3339") {
		t.Fatalf("time code=%d stderr=%q", code, stderr.String())
	}
}
