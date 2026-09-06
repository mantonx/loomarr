package fillervisualsafety_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestDecodeCoverageWithRealFFmpegPreservesSourcePTS(t *testing.T) {
	t.Parallel()

	prepared, ffmpeg := realDecoderSource(t, realDecoderOptions{
		rate: "10", rateNumerator: 10, rateDenominator: 1, durationMS: 3_000, lastFrameMS: 2_900, driftMS: 1,
		container: "mkv", codec: "ffv1", timeBaseDenominator: 1_000,
	})
	observed, evidence := decodeRealSource(t, prepared, ffmpeg)
	want := []int64{0, 1_000, 2_000, 2_900}
	if fmt.Sprint(observed) != fmt.Sprint(want) || len(evidence.Frames) != len(want) {
		t.Fatalf("observed PTS = %v, want %v", observed, want)
	}
}

func TestDecodeCoverageWithRealFFmpegHonorsCollapsedTerminalPlan(t *testing.T) {
	t.Parallel()

	prepared, ffmpeg := realDecoderSource(t, realDecoderOptions{
		rate: "25", rateNumerator: 25, rateDenominator: 1, durationMS: 3_080, lastFrameMS: 3_040, driftMS: 300,
		container: "mkv", codec: "ffv1", timeBaseDenominator: 1_000,
	})
	if got := prepared.Plan.Points; fmt.Sprint(got) != fmt.Sprint([]fillervisualsafety.CoveragePoint{
		{Ordinal: 0, RequestedMS: 0}, {Ordinal: 1, RequestedMS: 1_000},
		{Ordinal: 2, RequestedMS: 2_000}, {Ordinal: 3, RequestedMS: 3_040},
	}) {
		t.Fatalf("precondition: collapsed plan = %#v", got)
	}
	observed, evidence := decodeRealSource(t, prepared, ffmpeg)
	want := []int64{0, 1_000, 2_000, 3_040}
	if fmt.Sprint(observed) != fmt.Sprint(want) || len(evidence.Frames) != len(want) {
		t.Fatalf("observed PTS = %v, want %v", observed, want)
	}
}

func TestDecodeCoverageWithRealFFmpegSelectsRoundedMillisecondTerminal(t *testing.T) {
	t.Parallel()

	// The exact terminal PTS is 3.069733 seconds, whose authority timestamp is
	// 3,070 ms. Comparing the unrounded PTS to 3.070 silently drops the frame.
	prepared, ffmpeg := realDecoderSource(t, realDecoderOptions{
		rate: "30000/1001", rateNumerator: 30_000, rateDenominator: 1_001,
		durationMS: 3_080, lastFrameMS: 3_070, driftMS: 300,
		container: "mp4", codec: "h264", encoder: "libx264", timeBaseDenominator: 30_000,
	})
	observed, evidence := decodeRealSource(t, prepared, ffmpeg)
	want := []int64{0, 1_001, 2_002, 3_070}
	if fmt.Sprint(observed) != fmt.Sprint(want) || len(evidence.Frames) != len(want) {
		t.Fatalf("observed PTS = %v, want %v", observed, want)
	}
}

type realDecoderOptions struct {
	rate, container, codec, encoder                         string
	rateNumerator, rateDenominator, durationMS, lastFrameMS int64
	driftMS, timeBaseDenominator                            int64
}

func realDecoderSource(t *testing.T, options realDecoderOptions) (*fillervisualsafety.PreparedSource, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := t.TempDir() + "/source." + options.container
	filter := fmt.Sprintf("testsrc2=size=16x16:rate=%s:duration=%.3f", options.rate, float64(options.durationMS)/1_000)
	encoder := options.encoder
	if encoder == "" {
		encoder = options.codec
	}
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", filter, "-an", "-c:v", encoder}
	if options.container == "mp4" {
		arguments = append(arguments, "-video_track_timescale", fmt.Sprint(options.timeBaseDenominator))
	}
	arguments = append(arguments, "-y", path)
	generate := exec.Command(ffmpeg, arguments...)
	if output, commandErr := generate.CombinedOutput(); commandErr != nil {
		t.Fatalf("generate source: %v: %s", commandErr, output)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	authority, err := fillervisualsafety.SealSourceAuthority(fillervisualsafety.SourceAuthority{
		SourceID: "real-decoder-source", SourceSHA256: fmt.Sprintf("%x", digest), SourceBytes: int64(len(raw)),
		DurationMS: options.durationMS, PolicySHA256: repeatedDigest("c"), Implementation: "real-decoder-source-v1",
		Video: fillervisualsafety.VideoStreamIdentity{
			Index: 0, Codec: options.codec, Width: 16, Height: 16, FirstFrameMS: 0, LastFrameMS: options.lastFrameMS,
			FrameRateNumerator: options.rateNumerator, FrameRateDenominator: options.rateDenominator,
			TimeBaseNumerator: 1, TimeBaseDenominator: options.timeBaseDenominator, DurationMS: options.durationMS,
		},
		Probe: fillervisualsafety.ToolIdentity{
			Name: "ffprobe", Version: "synthetic-fixture", ExecutableSHA256: repeatedDigest("d"),
		},
		MeasuredAt: time.Date(2026, time.September, 4, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := fillervisualsafety.SealCoverageProfile(fillervisualsafety.CoverageProfile{
		Implementation: "real-decoder-profile-v1", MaximumSourceDurationMS: options.durationMS,
		ObservationIntervalMS: 1_000, MaximumTimestampDriftMS: options.driftMS,
		MaximumObservations: 8, MinimumCoveredExposureMS: 1_001 + 2*options.driftMS,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fillervisualsafety.Prepare(context.Background(), fillervisualsafety.SourceRequest{Authority: authority, Path: path}, profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	return prepared, ffmpeg
}

func decodeRealSource(t *testing.T, prepared *fillervisualsafety.PreparedSource, ffmpeg string) ([]int64, fillervisualsafety.CoverageEvidence) {
	t.Helper()
	var observed []int64
	evidence, err := fillervisualsafety.DecodeCoverage(context.Background(), prepared, ffmpeg,
		func(_ context.Context, frame fillervisualsafety.FrameEvidence, raw []byte) error {
			if len(raw) != 16*16*3 {
				t.Fatalf("RGB frame bytes = %d", len(raw))
			}
			observed = append(observed, frame.ObservedMS)
			return nil
		})
	if err != nil {
		t.Fatalf("DecodeCoverage(real ffmpeg) error = %v", err)
	}
	return observed, evidence
}
