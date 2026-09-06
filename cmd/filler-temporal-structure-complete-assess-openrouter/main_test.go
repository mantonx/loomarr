package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func TestRunExecutesCompleteVideoFamilyWithExplicitAuthority(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	var stdout, stderr bytes.Buffer
	called := false
	args := []string{
		"--window-set", "manifest.json", "--preflight", "preflight.json", "--snapshot", "snapshot.json", "--model", "vendor/model",
		"--model-family", "family-a", "--provider", "Provider", "--provider-slug", "provider",
		"--assessor-id", "assessor-a", "--reasoning-mode", "disabled", "--maximum-input-tokens", "1000",
		"--reservation-nanousd", "100000000", "--max-requests", "28", "--max-spend-nanousd", "2800000000",
		"--ledger", "ledger.db", "--evidence", "evidence", "--media-root", "media", "--out", "result.json",
	}
	code := run(args, &stdout, &stderr, capabilities{execute: func(_ context.Context, config commandConfig) (commandResult, error) {
		called = true
		if config.MaxRequests != 28 || config.MaxSpendNanoUSD != 2_800_000_000 || config.MediaRoot != "media" || config.APIKey != "test-key" {
			t.Fatalf("config=%+v", config)
		}
		return commandResult{Cases: 28, ProviderRequests: 28, ChargedNanoUSD: 100, AccountedNanoUSD: 100, ArtifactFileSHA256: strings.Repeat("a", 64)}, nil
	}})
	if code != 0 || !called || stderr.Len() != 0 || !strings.Contains(stdout.String(), "assessed 28 cases serially in 28 provider requests") ||
		!strings.Contains(stdout.String(), "training=false production=false") {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunRequiresCompleteVideoFamilyAuthority(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, capabilities{}); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "credential") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestValidateAuthorizedCompleteRunRequiresExactRequestsAndReservableSpend(t *testing.T) {
	manifest := fillerreview.TemporalStructureWindowSetManifest{Cases: make([]fillerreview.TemporalStructureWindowSetPublicCase, fillerreview.TemporalStructureWindowCorpusCases)}
	if err := validateAuthorizedCompleteRun(manifest, 28, 100_000_000, 2_800_000_000); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthorizedCompleteRun(manifest, 30, 100_000_000, 3_000_000_000); err == nil || !strings.Contains(err.Error(), "requires exactly 28") {
		t.Fatalf("request error=%v", err)
	}
	if err := validateAuthorizedCompleteRun(manifest, 28, 100_000_000, 2_700_000_000); err == nil || !strings.Contains(err.Error(), "cannot reserve all") {
		t.Fatalf("aggregate error=%v", err)
	}
}
