package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func TestRunPublishesShortLongShadowSummary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	args := []string{
		"--window-set", "manifest.json", "--window-certificate", "certificate.json",
		"--complete-decisions", "complete.json", "--window-decisions", "windows.json",
		"--compared-at", "2026-09-13T09:00:00Z", "--out", "shadow.json",
	}
	code := run(args, &stdout, &stderr, capabilities{publish: func(config fillerreview.TemporalStructureShortLongShadowConfig) (fillerreview.TemporalStructureShortLongShadowArtifact, string, error) {
		called = true
		if config.WindowSetManifestPath != "manifest.json" || config.WindowCertificationPath != "certificate.json" ||
			config.CompleteDecisionSetPath != "complete.json" || config.WindowDecisionSetPath != "windows.json" || config.OutputPath != "shadow.json" {
			t.Fatalf("config=%+v", config)
		}
		return fillerreview.TemporalStructureShortLongShadowArtifact{Report: fillerstructurewindowcert.ShadowReport{
			Status: fillerstructurewindowcert.ShadowStatusPassed, PassedCases: 28,
			ExpectedAliases: make([]string, 28), NextAction: "issue_separately_reviewed_long_reel_materialization_authority",
		}}, strings.Repeat("a", 64), nil
	}})
	if code != 0 || !called || stderr.Len() != 0 || !strings.Contains(stdout.String(), "passed; 28/28 cases agree") ||
		!strings.Contains(stdout.String(), "training=false production=false materialization=false") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunRequiresShortLongShadowInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "both decision sets") {
		t.Fatalf("missing code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"--window-set", "manifest.json", "--window-certificate", "certificate.json",
		"--complete-decisions", "complete.json", "--window-decisions", "windows.json",
		"--compared-at", "now", "--out", "shadow.json",
	}
	if code := run(args, &stdout, &stderr, capabilities{}); code != 2 || !strings.Contains(stderr.String(), "--compared-at must be RFC3339") {
		t.Fatalf("time code=%d stderr=%q", code, stderr.String())
	}
}
