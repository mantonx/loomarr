package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestRunPublishesWindowDecisionSummary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	args := []string{
		"--window-set", "manifest.json", "--first-family", "first.json", "--second-family", "second.json",
		"--decided-at", "2026-09-13T07:00:00Z", "--out", "decisions.json",
	}
	code := run(args, &stdout, &stderr, capabilities{publish: func(config fillerreview.TemporalStructureWindowShadowDecisionSetConfig) (fillerreview.TemporalStructureShadowDecisionSet, string, error) {
		called = true
		if config.WindowSetManifestPath != "manifest.json" || config.FirstFamilyPath != "first.json" ||
			config.SecondFamilyPath != "second.json" || config.OutputPath != "decisions.json" {
			t.Fatalf("config=%+v", config)
		}
		return fillerreview.TemporalStructureShadowDecisionSet{
			Families: []fillerreview.TemporalStructureShadowDecisionFamily{{}, {}},
			Cases: []fillerreview.TemporalStructureShadowDecisionCase{
				{Artifact: fillerstructure.Artifact{Decision: fillerstructure.Decision{Status: fillerstructure.StatusConfirmed}}},
				{Artifact: fillerstructure.Artifact{Decision: fillerstructure.Decision{Status: fillerstructure.StatusHeld}}},
			},
		}, strings.Repeat("a", 64), nil
	}})
	if code != 0 || !called || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "1/2 confirmed across 2 model families") ||
		!strings.Contains(stdout.String(), "training=false production=false") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunRequiresCompleteWindowDecisionInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "two family results") {
		t.Fatalf("missing code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"--window-set", "manifest.json", "--first-family", "first.json", "--second-family", "second.json",
		"--decided-at", "now", "--out", "decisions.json",
	}
	if code := run(args, &stdout, &stderr, capabilities{}); code != 2 || !strings.Contains(stderr.String(), "--decided-at must be RFC3339") {
		t.Fatalf("time code=%d stderr=%q", code, stderr.String())
	}
}
