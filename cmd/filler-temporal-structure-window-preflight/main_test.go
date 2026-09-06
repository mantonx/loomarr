package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func TestRunRequiresCompleteProviderFreePreflightAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "both production duration ceilings") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunStopsPaidSequenceWhenEnvelopeIsNotRepresented(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	code := run([]string{
		"--window-set", "manifest.json", "--suite", "suite.json",
		"--short-source-ceiling-ms", "120000", "--long-source-ceiling-ms", "300000", "--out", "preflight.json",
	}, &stdout, &stderr, capabilities{publish: func(config fillerreview.TemporalStructureWindowPreflightConfig) (fillerreview.TemporalStructureWindowPreflight, string, error) {
		called = true
		if config.ShortSourceCeilingMS != 120_000 || config.IntendedLongSourceCeilingMS != 300_000 || config.OutputPath != "preflight.json" {
			t.Fatalf("config=%+v", config)
		}
		return fillerreview.TemporalStructureWindowPreflight{
			Status: fillerreview.TemporalStructureWindowPreflightBlocked, Cases: 28,
			WindowRequestsPerFamily: 66, CompleteVideoRequestsPerFamily: 28, TotalProviderRequests: 188,
			MinimumObservedSourceDurationMS: 180_833, MaximumObservedSourceDurationMS: 301_020,
			MinimumObservedWindowsPerSource: 2, MaximumObservedWindowsPerSource: 3,
			MinimumObservedWindowBytes: 1_000, MaximumObservedWindowBytes: 30_000_000,
			MinimumRequiredEnvelopeEdgeCases: 2, NextAction: "extend_and_rerender_sealed_window_corpus",
		}, strings.Repeat("a", 64), nil
	}})
	if code != 1 || !called || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "66 windows/family + 28 complete videos/family = 188 serial provider requests") ||
		!strings.Contains(stdout.String(), "next=extend_and_rerender_sealed_window_corpus") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunAllowsPaidSequenceOnlyForReadyEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--window-set", "manifest.json", "--suite", "suite.json",
		"--short-source-ceiling-ms", "120000", "--long-source-ceiling-ms", "300000", "--out", "preflight.json",
	}, &stdout, &stderr, capabilities{publish: func(fillerreview.TemporalStructureWindowPreflightConfig) (fillerreview.TemporalStructureWindowPreflight, string, error) {
		return fillerreview.TemporalStructureWindowPreflight{
			Status: fillerreview.TemporalStructureWindowPreflightReady, ReadyForPaidCertification: true,
			Cases: 28, WindowRequestsPerFamily: 66, CompleteVideoRequestsPerFamily: 28, TotalProviderRequests: 188,
			MinimumObservedSourceDurationMS: 120_001, MaximumObservedSourceDurationMS: 300_000,
			MinimumObservedWindowsPerSource: 2, MaximumObservedWindowsPerSource: 3,
			MinimumObservedWindowBytes: 1_000, MaximumObservedWindowBytes: 30_000_000,
			LowerEnvelopeEdgeCases: 2, UpperEnvelopeEdgeCases: 2, MinimumRequiredEnvelopeEdgeCases: 2,
			NextAction: "run_two_truth_blind_window_families",
		}, strings.Repeat("a", 64), nil
	}})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "preflight: ready") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
