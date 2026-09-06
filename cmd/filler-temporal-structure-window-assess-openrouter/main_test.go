package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func TestRunRequiresCompletePaidWindowFamilyAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "positive request/cost ceilings") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestValidateAuthorizedWindowRunRequiresEveryWindowWithinExactRequestCeiling(t *testing.T) {
	manifest := fillerreview.TemporalStructureWindowSetManifest{Cases: make([]fillerreview.TemporalStructureWindowSetPublicCase, fillerreview.TemporalStructureWindowCorpusCases)}
	for index := range manifest.Cases {
		manifest.Cases[index].Windows = make([]fillerreview.TemporalStructureWindowSetWindow, 2)
	}
	if windows, err := validateAuthorizedWindowRun(manifest, 56, 100_000_000, 5_600_000_000); err != nil || windows != 56 {
		t.Fatalf("windows=%d error=%v", windows, err)
	}
	if _, err := validateAuthorizedWindowRun(manifest, 30, 100_000_000, 3_000_000_000); err == nil || !strings.Contains(err.Error(), "requires exactly 56") {
		t.Fatalf("request ceiling error=%v", err)
	}
	if _, err := validateAuthorizedWindowRun(manifest, 56, 100_000_000, 50_000_000); err == nil || !strings.Contains(err.Error(), "reservation exceeds") {
		t.Fatalf("spend ceiling error=%v", err)
	}
	if _, err := validateAuthorizedWindowRun(manifest, 56, 100_000_000, 5_500_000_000); err == nil || !strings.Contains(err.Error(), "cannot reserve all") {
		t.Fatalf("aggregate ceiling error=%v", err)
	}
}

func TestRunReportsCompleteSerialWindowFamily(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	var stdout, stderr bytes.Buffer
	called := false
	args := []string{
		"--window-set", "manifest.json", "--preflight", "preflight.json", "--snapshot", "snapshot.json",
		"--model", "vendor/model", "--model-family", "family-a",
		"--provider", "Provider", "--provider-slug", "provider/route", "--assessor-id", "assessor-a",
		"--reasoning-mode", "disabled", "--maximum-input-tokens", "20000",
		"--reservation-nanousd", "100000000", "--max-requests", "66", "--max-spend-nanousd", "6600000000",
		"--ledger", "ledger.db", "--evidence", "evidence", "--out", "family.json",
	}
	code := run(args, &stdout, &stderr, capabilities{execute: func(_ context.Context, config commandConfig) (commandResult, error) {
		called = true
		if config.MaxRequests != 66 || config.MaxSpendNanoUSD != 6_600_000_000 ||
			config.ReservationNanoUSD != 100_000_000 || config.APIKey != "test-key" || config.OutputPath != "family.json" {
			t.Fatalf("config=%+v", config)
		}
		return commandResult{Cases: 28, Windows: 66, ProviderRequests: 66, CompleteStitches: 27, HeldStitches: 1,
			ChargedNanoUSD: 123_000_000, AccountedNanoUSD: 123_000_000,
			EstimatedMaximumChargeNanoUSD: 80_000_000, ArtifactFileSHA256: strings.Repeat("a", 64)}, nil
	}})
	if code != 0 || !called || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "assessed 28 cases/66 windows serially in 66 provider requests; complete=27 held=1") ||
		!strings.Contains(stdout.String(), "charged=123000000 accounted=123000000 nano-USD unknown=0") ||
		!strings.Contains(stdout.String(), "training=false production=false") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}
