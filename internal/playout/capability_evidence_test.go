package playout

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDetectObservedWithEvidenceValidatesAndReusesHardwareCapacity(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	detectCalls, validationCalls := 0, 0
	deps := capabilityEvidenceDependencies{
		now:         func() time.Time { return now },
		fingerprint: func(context.Context, string, string, Profile) (string, error) { return "host-a", nil },
		detect: func(context.Context, string, Profile, string) Capacity {
			detectCalls++
			return Capacity{Chosen: EncoderNVENC, MaxChannels: 7}
		},
		validate: func(context.Context, string, Encoder, Profile) Capability {
			validationCalls++
			return Capability{Encoder: EncoderNVENC, Works: true}
		},
	}
	first, reused := detectObservedWithEvidence(t.Context(), "ffmpeg", DefaultProfile(), "nvidia", root, deps)
	if reused || first.Chosen != EncoderNVENC || first.MaxChannels != 7 || detectCalls != 1 || validationCalls != 0 {
		t.Fatalf("first detection = %+v reused=%v detect=%d validate=%d", first, reused, detectCalls, validationCalls)
	}
	second, reused := detectObservedWithEvidence(t.Context(), "ffmpeg", DefaultProfile(), "nvidia", root, deps)
	if !reused || second.Chosen != EncoderNVENC || second.MaxChannels != 7 || detectCalls != 1 || validationCalls != 1 {
		t.Fatalf("reused detection = %+v reused=%v detect=%d validate=%d", second, reused, detectCalls, validationCalls)
	}
}

func TestMatchingCapabilityEvidenceDoesNotRunEncoderValidation(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	if err := storeCapabilityEvidence(root, capabilityEvidence{
		Version: capabilityEvidenceVersion, Fingerprint: "host-a",
		Encoder: EncoderNVENC, MaxChannels: 7, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	validationCalls := 0
	capacity, _, ok := matchingCapabilityEvidence(
		t.Context(), "ffmpeg", DefaultProfile(), "nvidia", root,
		capabilityEvidenceDependencies{
			now:         func() time.Time { return now },
			fingerprint: func(context.Context, string, string, Profile) (string, error) { return "host-a", nil },
			validate: func(context.Context, string, Encoder, Profile) Capability {
				validationCalls++
				return Capability{Encoder: EncoderNVENC, Works: true}
			},
		},
	)
	if !ok || capacity.Chosen != EncoderNVENC || capacity.MaxChannels != 7 || validationCalls != 0 {
		t.Fatalf("matching evidence = %+v ok=%v validation calls=%d", capacity, ok, validationCalls)
	}
}

func TestLoadMatchingCapabilityEvidenceBoundsAStalledIdentityCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexec sleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	_, ok := LoadMatchingObservedCapabilityEvidence(
		t.Context(), ffmpeg, DefaultProfile(), "test-gpu", t.TempDir(),
	)
	if elapsed := time.Since(before); ok || elapsed >= time.Second {
		t.Fatalf("matching stalled executable: ok=%v elapsed=%s, want miss below one second", ok, elapsed)
	}
}

func TestDetectObservedWithEvidenceRejectsMismatchExpiryAndFailedValidation(t *testing.T) {
	for _, test := range []struct {
		name       string
		secondHost string
		advance    time.Duration
		works      bool
	}{
		{name: "fingerprint changed", secondHost: "host-b", works: true},
		{name: "expired", secondHost: "host-a", advance: capabilityEvidenceMaxAge + time.Second, works: true},
		{name: "validation failed", secondHost: "host-a", works: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
			host := "host-a"
			detectCalls, validationCalls := 0, 0
			deps := capabilityEvidenceDependencies{
				now:         func() time.Time { return now },
				fingerprint: func(context.Context, string, string, Profile) (string, error) { return host, nil },
				detect: func(context.Context, string, Profile, string) Capacity {
					detectCalls++
					return Capacity{Chosen: EncoderNVENC, MaxChannels: 5}
				},
				validate: func(context.Context, string, Encoder, Profile) Capability {
					validationCalls++
					return Capability{Encoder: EncoderNVENC, Works: test.works}
				},
			}
			_, _ = detectObservedWithEvidence(t.Context(), "ffmpeg", DefaultProfile(), "nvidia", root, deps)
			host = test.secondHost
			now = now.Add(test.advance)
			_, reused := detectObservedWithEvidence(t.Context(), "ffmpeg", DefaultProfile(), "nvidia", root, deps)
			if reused || detectCalls != 2 {
				t.Fatalf("reused=%v detect calls=%d, want fresh full detection", reused, detectCalls)
			}
			wantValidation := 0
			if test.secondHost == "host-a" && test.advance <= capabilityEvidenceMaxAge {
				wantValidation = 1
			}
			if validationCalls != wantValidation {
				t.Fatalf("validation calls=%d, want %d", validationCalls, wantValidation)
			}
		})
	}
}
