package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func TestRunRequiresCompleteAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr, projectCapabilities{})
	if code != 2 || !strings.Contains(stderr.String(), "every corpus") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunRejectsNonRFC3339Time(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{
		"--corpus-manifest", "draft.json", "--packets", "packets.jsonl", "--corpus-root", "derivatives",
		"--corpus-split", "development", "--evidence-version", "evidence-v1", "--expected-corpus-cases", "48",
		"--evidence-manifest", "evidence.json", "--evidence-private-map", "map.json", "--transcripts", "transcripts.jsonl",
		"--structure-manifest", "structure.json", "--structure-authority", "authority.json", "--expected-structure-cases", "36",
		"--policy", "policy.json", "--projected-at", "tomorrow", "--output", "report.json",
	}
	code := run(args, &stdout, &stderr, projectCapabilities{})
	if code != 2 || !strings.Contains(stderr.String(), "RFC3339") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunPublishesProjectionAndPrintsOnlyOpaqueSummary(t *testing.T) {
	privatePhrase := "restricted fixture phrase"
	var stdout, stderr bytes.Buffer
	args := validProjectArgs()
	called := false
	code := run(args, &stdout, &stderr, projectCapabilities{publish: func(config fillerreview.TemporalSpokenSafetyConfig) (fillerreview.TemporalSpokenSafetyReport, string, error) {
		called = true
		if config.ExpectedCorpusCases != 48 || config.CorpusSplit != "development" || config.ExpectedStructureCases != 36 || config.OutputPath != "report.json" {
			t.Fatalf("config = %+v", config)
		}
		return fillerreview.TemporalSpokenSafetyReport{
			Sources: 54, ProhibitedSources: 1, CoverageHoldSources: 7, NoSignalObservedSources: 46,
			ProhibitedCases: 2, CoverageHoldCases: 3,
		}, strings.Repeat("a", 64), nil
	}})
	if code != 0 || !called || strings.Contains(stdout.String(), privatePhrase) || strings.Contains(stderr.String(), privatePhrase) {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "54 sources (1 prohibited, 7 coverage holds, 46 no-signal observations); 5 derivative cases held") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunReportsPublishFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(validProjectArgs(), &stdout, &stderr, projectCapabilities{publish: func(fillerreview.TemporalSpokenSafetyConfig) (fillerreview.TemporalSpokenSafetyReport, string, error) {
		return fillerreview.TemporalSpokenSafetyReport{}, "", errors.New("authority drift")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "authority drift") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func validProjectArgs() []string {
	return []string{
		"--corpus-manifest", "draft.json", "--packets", "packets.jsonl", "--corpus-root", "derivatives",
		"--corpus-split", "development", "--evidence-version", "evidence-v1", "--expected-corpus-cases", "48",
		"--evidence-manifest", "evidence.json", "--evidence-private-map", "map.json", "--transcripts", "transcripts.jsonl",
		"--structure-manifest", "structure.json", "--structure-authority", "authority.json", "--expected-structure-cases", "36",
		"--policy", "policy.json", "--projected-at", "2026-09-02T16:00:00Z", "--output", "report.json",
	}
}
