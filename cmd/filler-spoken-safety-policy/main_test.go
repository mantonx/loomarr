package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func TestRunPolicyPublishesWithoutEchoingPhrase(t *testing.T) {
	privatePhrase := "restricted fixture phrase"
	var stdout, stderr bytes.Buffer
	called := false
	code := run([]string{"--policy-id", "policy-fixture", "--generated-at", "2026-09-02T12:00:00Z", "--output", "policy.json"}, strings.NewReader(`{"class":"prohibited","phrase":"`+privatePhrase+`"}`+"\n"), &stdout, &stderr, policyCapabilities{
		random: bytes.NewReader(make([]byte, 12)),
		publish: func(config fillerreview.TemporalSpokenSafetyPolicyBuildConfig) (fillerreview.TemporalSpokenSafetyPolicy, string, error) {
			called = true
			if len(config.ProhibitedPhrases) != 1 || config.ProhibitedPhrases[0] != privatePhrase || len(config.AmbiguousPhrases) != 0 {
				t.Fatalf("config = %+v", config)
			}
			return fillerreview.TemporalSpokenSafetyPolicy{Rules: []fillerreview.TemporalSpokenSafetyPolicyRule{{ID: "opaque"}}}, strings.Repeat("a", 64), nil
		},
	})
	if code != 0 || !called || strings.Contains(stdout.String(), privatePhrase) || strings.Contains(stderr.String(), privatePhrase) {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunPolicyRejectsUnknownClassBeforePublishing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--policy-id", "policy-fixture", "--generated-at", "2026-09-02T12:00:00Z", "--output", "policy.json"}, strings.NewReader("{\"class\":\"clear\",\"phrase\":\"text\"}\n"), &stdout, &stderr, policyCapabilities{publish: func(fillerreview.TemporalSpokenSafetyPolicyBuildConfig) (fillerreview.TemporalSpokenSafetyPolicy, string, error) {
		t.Fatal("publish called")
		return fillerreview.TemporalSpokenSafetyPolicy{}, "", nil
	}})
	if code != 1 || !strings.Contains(stderr.String(), "unknown class") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunPolicyRoutesExplicitPrefixModeWithoutEchoingPhrase(t *testing.T) {
	privatePhrase := "private prefix"
	var stdout, stderr bytes.Buffer
	code := run([]string{"--policy-id", "policy-fixture", "--generated-at", "2026-09-02T12:00:00Z", "--output", "policy.json"}, strings.NewReader(`{"class":"prohibited","mode":"token_prefix","phrase":"`+privatePhrase+`"}`+"\n"), &stdout, &stderr, policyCapabilities{
		publish: func(config fillerreview.TemporalSpokenSafetyPolicyBuildConfig) (fillerreview.TemporalSpokenSafetyPolicy, string, error) {
			if len(config.ProhibitedPrefixes) != 1 || config.ProhibitedPrefixes[0] != privatePhrase || len(config.ProhibitedPhrases) != 0 {
				t.Fatalf("config = %+v", config)
			}
			return fillerreview.TemporalSpokenSafetyPolicy{Rules: []fillerreview.TemporalSpokenSafetyPolicyRule{{ID: "opaque"}}}, strings.Repeat("b", 64), nil
		},
	})
	if code != 0 || strings.Contains(stdout.String(), privatePhrase) || strings.Contains(stderr.String(), privatePhrase) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
