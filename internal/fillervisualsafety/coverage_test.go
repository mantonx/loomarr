package fillervisualsafety_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestPlanCoverageIncludesCompleteTimelineEdges(t *testing.T) {
	authority := visualAuthority(t, 10_050)
	profile := visualProfile(t)

	plan, err := fillervisualsafety.PlanCoverage(authority, profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := fillervisualsafety.ValidateCoveragePlan(plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Points) != 12 || plan.Points[0].RequestedMS != 0 || plan.Points[len(plan.Points)-1].RequestedMS != 10_049 {
		t.Fatalf("unexpected complete coverage plan: %#v", plan.Points)
	}
	if plan.MaximumPlannedGapMS != 1_000 || plan.Profile.MinimumCoveredExposureMS != 1_021 {
		t.Fatalf("unexpected gap contract: %#v", plan)
	}

	repeated, err := fillervisualsafety.PlanCoverage(authority, profile)
	if err != nil || repeated.SHA256 != plan.SHA256 {
		t.Fatalf("coverage plan is not deterministic: %#v %v", repeated, err)
	}
}

func TestPlanCoverageUsesMeasuredFirstAndLastFrames(t *testing.T) {
	authority := visualAuthority(t, 10_050)
	authority.Video.FirstFrameMS = 17
	authority.Video.LastFrameMS = 10_017
	authority.SHA256 = fillervisualsafety.SourceAuthoritySHA256(authority)
	plan, err := fillervisualsafety.PlanCoverage(authority, visualProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Points[0].RequestedMS != 17 || plan.Points[len(plan.Points)-1].RequestedMS != 10_017 || len(plan.Points) != 11 {
		t.Fatalf("plan did not retain measured frame edges: %#v", plan.Points)
	}
}

func TestPlanCoverageCollapsesAnOverlappingTerminalDriftWindow(t *testing.T) {
	authority := visualAuthority(t, 10_007)
	profile := visualProfile(t)

	plan, err := fillervisualsafety.PlanCoverage(authority, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Points) != 11 || plan.Points[9].RequestedMS != 9_000 || plan.Points[10].RequestedMS != 10_006 {
		t.Fatalf("terminal drift window was not collapsed: %#v", plan.Points)
	}
	if plan.MaximumPlannedGapMS != 1_006 || plan.MaximumPlannedGapMS >= profile.MinimumCoveredExposureMS {
		t.Fatalf("collapsed terminal gap is not covered by the profile: %#v", plan)
	}
}

func TestSourceAuthorityRejectsAnInventedTerminalFrame(t *testing.T) {
	authority := visualAuthority(t, 10_050)
	authority.Video.LastFrameMS = authority.DurationMS
	authority.SHA256 = fillervisualsafety.SourceAuthoritySHA256(authority)
	if err := fillervisualsafety.ValidateSourceAuthority(authority); err == nil {
		t.Fatal("expected a frame timestamp at the half-open source end to fail")
	}
}

func TestPrepareSnapshotsExactSourceBeforeAdaptersCanUseIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.mp4")
	original := []byte("exact visual source bytes")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(original)
	authority := visualAuthority(t, 3_050)
	authority.SourceSHA256 = hex.EncodeToString(digest[:])
	authority.SourceBytes = int64(len(original))
	authority.SHA256 = fillervisualsafety.SourceAuthoritySHA256(authority)
	if err := fillervisualsafety.ValidateSourceAuthority(authority); err != nil {
		t.Fatal(err)
	}

	prepared, err := fillervisualsafety.Prepare(t.Context(), fillervisualsafety.SourceRequest{Authority: authority, Path: path}, visualProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if prepared.SnapshotPath == path || !filepath.IsAbs(prepared.SnapshotPath) || prepared.Plan.SourceAuthoritySHA256 != authority.SHA256 {
		t.Fatalf("source was not privately prepared: %#v", prepared)
	}
	if err := os.WriteFile(path, []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotted, err := os.ReadFile(prepared.SnapshotPath)
	if err != nil || string(snapshotted) != string(original) {
		t.Fatalf("prepared bytes followed caller mutation: %q %v", snapshotted, err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prepared.SnapshotPath); !os.IsNotExist(err) {
		t.Fatalf("private snapshot survived close: %v", err)
	}
}

func TestPrepareRejectsSourceIdentityDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte("different bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fillervisualsafety.Prepare(t.Context(), fillervisualsafety.SourceRequest{Authority: visualAuthority(t, 3_050), Path: path}, visualProfile(t)); err == nil {
		t.Fatal("expected source identity drift to fail")
	}
}

func TestCoverageProfileCannotClaimAnUnobservedShorterExposure(t *testing.T) {
	profile := fillervisualsafety.CoverageProfile{
		Implementation: "dense-frame-v1", MaximumSourceDurationMS: 20_000,
		ObservationIntervalMS: 1_000, MaximumTimestampDriftMS: 10,
		MaximumObservations: 100, MinimumCoveredExposureMS: 1_020,
	}
	if _, err := fillervisualsafety.SealCoverageProfile(profile); err == nil {
		t.Fatal("expected a profile whose display floor does not exceed its worst observed gap to fail")
	}
}

func TestCoverageEvidenceRequiresEveryBoundFrame(t *testing.T) {
	authority := visualAuthority(t, 3_050)
	plan, err := fillervisualsafety.PlanCoverage(authority, visualProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	frames := visualFrames(plan)
	decoder := fillervisualsafety.ToolIdentity{Name: "ffmpeg", Version: "7.1", ExecutableSHA256: strings.Repeat("e", 64)}

	evidence, err := fillervisualsafety.SealCoverageEvidence(plan, decoder, frames, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := fillervisualsafety.ValidateCoverageEvidence(plan, evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.MaximumObservedGapMS != 1_000 || len(evidence.Frames) != len(plan.Points) {
		t.Fatalf("unexpected coverage evidence: %#v", evidence)
	}

	for _, test := range []struct {
		name   string
		mutate func([]fillervisualsafety.FrameEvidence) []fillervisualsafety.FrameEvidence
	}{
		{name: "missing", mutate: func(items []fillervisualsafety.FrameEvidence) []fillervisualsafety.FrameEvidence {
			return items[:len(items)-1]
		}},
		{name: "timestamp drift", mutate: func(items []fillervisualsafety.FrameEvidence) []fillervisualsafety.FrameEvidence {
			items[1].ObservedMS += 11
			return items
		}},
		{name: "wrong dimensions", mutate: func(items []fillervisualsafety.FrameEvidence) []fillervisualsafety.FrameEvidence {
			items[1].Width--
			return items
		}},
		{name: "wrong digest", mutate: func(items []fillervisualsafety.FrameEvidence) []fillervisualsafety.FrameEvidence {
			items[1].SHA256 = "no"
			return items
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := append([]fillervisualsafety.FrameEvidence(nil), frames...)
			if _, err := fillervisualsafety.SealCoverageEvidence(plan, decoder, test.mutate(candidate), true); err == nil {
				t.Fatal("expected incomplete or drifted coverage evidence to fail")
			}
		})
	}
	if _, err := fillervisualsafety.SealCoverageEvidence(plan, decoder, frames, false); err == nil {
		t.Fatal("expected incomplete decode to fail")
	}
}

func visualAuthority(t *testing.T, durationMS int64) fillervisualsafety.SourceAuthority {
	t.Helper()
	authority, err := fillervisualsafety.SealSourceAuthority(fillervisualsafety.SourceAuthority{
		SourceID: "source-1", SourceSHA256: strings.Repeat("a", 64), SourceBytes: 1_000_000,
		DurationMS: durationMS, PolicySHA256: strings.Repeat("b", 64), Implementation: "source-probe-v1",
		Video: fillervisualsafety.VideoStreamIdentity{
			Index: 0, Codec: "h264", Width: 960, Height: 720,
			FirstFrameMS: 0, LastFrameMS: durationMS - 1,
			FrameRateNumerator: 30, FrameRateDenominator: 1,
			TimeBaseNumerator: 1, TimeBaseDenominator: 90_000, DurationMS: durationMS,
		},
		Probe:      fillervisualsafety.ToolIdentity{Name: "ffprobe", Version: "7.1", ExecutableSHA256: strings.Repeat("c", 64)},
		MeasuredAt: time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func visualProfile(t *testing.T) fillervisualsafety.CoverageProfile {
	t.Helper()
	profile, err := fillervisualsafety.SealCoverageProfile(fillervisualsafety.CoverageProfile{
		Implementation: "dense-frame-v1", MaximumSourceDurationMS: 20_000,
		ObservationIntervalMS: 1_000, MaximumTimestampDriftMS: 10,
		MaximumObservations: 100, MinimumCoveredExposureMS: 1_021,
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func visualFrames(plan fillervisualsafety.CoveragePlan) []fillervisualsafety.FrameEvidence {
	frames := make([]fillervisualsafety.FrameEvidence, len(plan.Points))
	for index, point := range plan.Points {
		frames[index] = fillervisualsafety.FrameEvidence{
			Ordinal: point.Ordinal, RequestedMS: point.RequestedMS, ObservedMS: point.RequestedMS,
			SHA256: strings.Repeat(string(rune('d'+index%3)), 64), Bytes: 10_000,
			Width: plan.Video.Width, Height: plan.Video.Height,
		}
	}
	return frames
}
