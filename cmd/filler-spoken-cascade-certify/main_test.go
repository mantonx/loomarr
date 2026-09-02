package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func TestRunPublishesOnlyAnOpaqueAggregate(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	called := false
	code := run([]string{
		"--authority", "authority.json", "--results", "results.json",
		"--scored-at", "2026-09-02T18:00:00Z", "--output", "score.json",
	}, &stdout, &stderr, capabilities{publish: func(config fillersafetycert.Config) (fillersafetycert.Report, string, error) {
		called = true
		if config.AuthorityPath != "authority.json" || config.ResultsPath != "results.json" || config.OutputPath != "score.json" {
			t.Fatalf("config=%+v", config)
		}
		return fillersafetycert.Report{
			CertificationStatus: fillersafetycert.StatusPassed, DetectedPositiveSources: 59,
			PositiveFamilies: 59, SourceRecallExactLower95: .9505, CleanSources: 100,
		}, strings.Repeat("a", 64), nil
	}})
	if code != 0 || !called || !strings.Contains(stdout.String(), "59/59 positive families") ||
		strings.Contains(stdout.String(), "positiveIntervals") || stderr.Len() != 0 {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunRejectsMissingOrMutableTime(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || !strings.Contains(stderr.String(), "required") {
		t.Fatalf("missing code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	args := []string{"--authority", "authority.json", "--results", "results.json", "--scored-at", "now", "--output", "score.json"}
	if code := run(args, &stdout, &stderr, capabilities{}); code != 2 || !strings.Contains(stderr.String(), "RFC3339") {
		t.Fatalf("time code=%d stderr=%q", code, stderr.String())
	}
}
