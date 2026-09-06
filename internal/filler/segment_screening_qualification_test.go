package filler

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
)

func TestQualificationSegmentScreeningRuntimeRecordsTruthfulFiveAxisEvidence(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	evidenceRoot := filepath.Join(t.TempDir(), "segment-screening")
	repository := &memoryFillerRightsGrantRepository{}
	at := time.Date(2026, time.September, 15, 3, 0, 0, 0, time.UTC)
	clockCalls := 0
	runtime, err := NewQualificationSegmentScreeningRuntime(evidenceRoot, repository, func() time.Time {
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := runtime.Screen(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	want := map[SegmentScreeningAxis]struct {
		outcome SegmentScreeningOutcome
		reason  string
	}{
		ScreenVisualSafety:  {ScreenHold, "visual_safety_not_certified"},
		ScreenSpokenSafety:  {ScreenHold, "spoken_safety_not_certified"},
		ScreenWrittenSafety: {ScreenHold, "written_safety_not_certified"},
		ScreenRights:        {ScreenHold, "rights_unknown"},
		ScreenPlayback:      {ScreenPass, "playback_verified"},
	}
	if first.Passes() || len(first.Results) != len(want) || clockCalls != len(want) {
		t.Fatalf("qualification aggregate = %+v, clock calls = %d", first, clockCalls)
	}
	if first.Airworthiness.Profile != fillerairworthiness.ProfileAllAges ||
		first.Airworthiness.Verdict != fillerairworthiness.VerdictHold ||
		!slices.Contains(first.Airworthiness.ReasonCodes, fillerairworthiness.ReasonCoverageIncomplete) ||
		!slices.Contains(first.Airworthiness.ReasonCodes, fillerairworthiness.ReasonCertificationIncomplete) ||
		fillerairworthiness.ValidateDecision(first.Airworthiness) != nil {
		t.Fatalf("qualification Airworthiness = %+v", first.Airworthiness)
	}
	evidence, err := NewFileSegmentScreeningEvidenceRepository(evidenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range first.Results {
		expected := want[result.Axis]
		if result.Outcome != expected.outcome || result.ReasonCode != expected.reason {
			t.Fatalf("axis %q = %+v, want outcome=%q reason=%q", result.Axis, result, expected.outcome, expected.reason)
		}
		recorded, err := evidence.GetSegmentScreeningAxisEvidence(t.Context(), result.AuthoritySHA256)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(recorded.RawEvidence, []byte(filepath.Dir(media.EvidencePath))) {
			t.Fatalf("axis %q leaked a private media path", result.Axis)
		}
	}

	second, err := runtime.Screen(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	if second.SHA256 != first.SHA256 || clockCalls != len(want) {
		t.Fatalf("qualification replay changed: first=%s second=%s clock calls=%d", first.SHA256, second.SHA256, clockCalls)
	}
}

func TestQualificationSafetyHoldBindsExactEvidenceDerivative(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	evidenceRoot := filepath.Join(t.TempDir(), "segment-screening")
	runtime, err := NewQualificationSegmentScreeningRuntime(evidenceRoot, &memoryFillerRightsGrantRepository{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.Screen(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(media.EvidencePath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range body {
		body[index] = 'x'
	}
	if err := os.WriteFile(media.EvidencePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Screen(t.Context(), media); err == nil || !strings.Contains(err.Error(), "conflicts with its settled result") {
		t.Fatalf("evidence drift replay error = %v; first=%s", err, first.SHA256)
	}
}

func TestQualificationSegmentScreeningRuntimeRequiresItsAuthorities(t *testing.T) {
	repository := &memoryFillerRightsGrantRepository{}
	for _, test := range []struct {
		name string
		root string
		repo FillerRightsGrantRepository
		now  func() time.Time
	}{
		{name: "relative root", root: "relative", repo: repository, now: time.Now},
		{name: "missing rights repository", root: t.TempDir(), now: time.Now},
		{name: "missing clock", root: t.TempDir(), repo: repository},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewQualificationSegmentScreeningRuntime(test.root, test.repo, test.now); err == nil {
				t.Fatal("invalid qualification runtime was constructed")
			}
		})
	}
}
