package fillervisualsafety_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestDecodeCoverageConsumesEveryPlannedFrameAndReachesEOF(t *testing.T) {
	t.Parallel()

	source, profile := preparedDecoderSource(t)
	defer func() { _ = source.Close() }()
	arguments := t.TempDir() + "/arguments"
	ffmpeg := decoderExecutable(t, arguments, []string{"0", "1", "2", "2.999"}, 0)
	var consumed []fillervisualsafety.FrameEvidence
	evidence, err := fillervisualsafety.DecodeCoverage(context.Background(), source, ffmpeg,
		func(_ context.Context, frame fillervisualsafety.FrameEvidence, raw []byte) error {
			if len(raw) != 4*3*3 {
				t.Fatalf("frame bytes = %d", len(raw))
			}
			consumed = append(consumed, frame)
			return nil
		})
	if err != nil {
		t.Fatalf("DecodeCoverage() error = %v", err)
	}
	if len(consumed) != 4 || len(evidence.Frames) != 4 || evidence.MaximumObservedGapMS != 1_000 || !evidence.CompleteDecode {
		t.Fatalf("coverage evidence = %+v; consumed = %d", evidence, len(consumed))
	}
	if evidence.Frames[3].RequestedMS != 2_999 || evidence.Frames[3].ObservedMS != 2_999 {
		t.Fatalf("terminal frame = %+v", evidence.Frames[3])
	}
	if evidence.PlanSHA256 != source.Plan.SHA256 || evidence.Frames[0].SHA256 != fmt.Sprintf("%x", sha256.Sum256(make([]byte, 36))) {
		t.Fatal("coverage evidence does not bind the plan and raw RGB frame")
	}
	if err := fillervisualsafety.ValidateCoverageEvidence(source.Plan, evidence); err != nil {
		t.Fatalf("ValidateCoverageEvidence() error = %v", err)
	}
	if source.Plan.Profile.SHA256 != profile.SHA256 {
		t.Fatal("prepared source changed its coverage profile")
	}
	rawArguments, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	joined := string(rawArguments)
	for _, required := range []string{
		"-xerror", "-err_detect", "explode", "-max_error_rate", "0", "-copyts", "-noautorotate",
		"-map", "0:0", "-threads:v", "1", "-fps_mode", "passthrough", "-pix_fmt", "rgb24", "pipe:1",
		"isnan(prev_selected_t)+lt(selected_n\\,3)*gte(round(t*1000)\\,0+selected_n*1000)+gte(round(t*1000)\\,2999)",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("decoder arguments lack %q:\n%s", required, joined)
		}
	}
}

func TestDecodeCoverageFailsClosedOnFrameTimestampDecodeAndConsumerErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pts         []string
		exit        int
		consumerErr error
	}{
		"missing frame":   {pts: []string{"0", "1", "2"}},
		"extra frame":     {pts: []string{"0", "1", "2", "2.999", "2.999"}},
		"timestamp drift": {pts: []string{"0", "1.100", "2", "2.999"}},
		"decode failure":  {pts: []string{"0", "1", "2", "2.999"}, exit: 7},
		"consumer failure": {
			pts: []string{"0", "1", "2", "2.999"}, consumerErr: errors.New("model unavailable"),
		},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source, _ := preparedDecoderSource(t)
			defer func() { _ = source.Close() }()
			ffmpeg := decoderExecutable(t, t.TempDir()+"/arguments", test.pts, test.exit)
			evidence, err := fillervisualsafety.DecodeCoverage(context.Background(), source, ffmpeg,
				func(_ context.Context, _ fillervisualsafety.FrameEvidence, _ []byte) error { return test.consumerErr })
			if err == nil || evidence.SHA256 != "" {
				t.Fatalf("DecodeCoverage() = %+v, %v", evidence, err)
			}
		})
	}
}

func TestDecodeCoverageRevalidatesSnapshotAfterConsumerRuns(t *testing.T) {
	t.Parallel()

	source, _ := preparedDecoderSource(t)
	defer func() { _ = source.Close() }()
	ffmpeg := decoderExecutable(t, t.TempDir()+"/arguments", []string{"0", "1", "2", "2.999"}, 0)
	mutated := false
	_, err := fillervisualsafety.DecodeCoverage(context.Background(), source, ffmpeg,
		func(_ context.Context, _ fillervisualsafety.FrameEvidence, _ []byte) error {
			if !mutated {
				mutated = true
				return os.WriteFile(source.SnapshotPath, []byte("changed source bytes"), 0o600)
			}
			return nil
		})
	if err == nil || !strings.Contains(err.Error(), "prepared source") {
		t.Fatalf("DecodeCoverage() error = %v", err)
	}
}

func TestDecodeCoverageRejectsConsumerMutationOfFrameEvidence(t *testing.T) {
	t.Parallel()

	source, _ := preparedDecoderSource(t)
	defer func() { _ = source.Close() }()
	ffmpeg := decoderExecutable(t, t.TempDir()+"/arguments", []string{"0", "1", "2", "2.999"}, 0)
	_, err := fillervisualsafety.DecodeCoverage(context.Background(), source, ffmpeg,
		func(_ context.Context, _ fillervisualsafety.FrameEvidence, raw []byte) error {
			raw[0] = 1
			return nil
		})
	if err == nil || !strings.Contains(err.Error(), "mutated decoded evidence") {
		t.Fatalf("DecodeCoverage() error = %v", err)
	}
}

func TestDecodeCoverageHonorsCallerDeadline(t *testing.T) {
	t.Parallel()

	source, _ := preparedDecoderSource(t)
	defer func() { _ = source.Close() }()
	ffmpeg := testkit.POSIXExecutable(t, "ffmpeg", `
if [ "$1" = "-version" ]; then printf '%s\n' 'ffmpeg version blocking-test'; exit 0; fi
while :; do :; done
`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := fillervisualsafety.DecodeCoverage(ctx, source, ffmpeg,
		func(_ context.Context, _ fillervisualsafety.FrameEvidence, _ []byte) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DecodeCoverage() error = %v", err)
	}
}

func preparedDecoderSource(t *testing.T) (*fillervisualsafety.PreparedSource, fillervisualsafety.CoverageProfile) {
	t.Helper()
	path := t.TempDir() + "/source.mp4"
	raw := []byte("exact private source")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	authority, err := fillervisualsafety.SealSourceAuthority(fillervisualsafety.SourceAuthority{
		SourceID: "decoder-source", SourceSHA256: fmt.Sprintf("%x", digest), SourceBytes: int64(len(raw)),
		DurationMS: 3_000, PolicySHA256: repeatedDigest("a"), Implementation: "decoder-source-v1",
		Video: fillervisualsafety.VideoStreamIdentity{
			Index: 0, Codec: "h264", Width: 4, Height: 3, FirstFrameMS: 0, LastFrameMS: 2_999,
			FrameRateNumerator: 1, FrameRateDenominator: 1, TimeBaseNumerator: 1, TimeBaseDenominator: 1_000,
			DurationMS: 3_000,
		},
		Probe: fillervisualsafety.ToolIdentity{
			Name: "ffprobe", Version: "test", ExecutableSHA256: repeatedDigest("b"),
		},
		MeasuredAt: time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := fillervisualsafety.SealCoverageProfile(fillervisualsafety.CoverageProfile{
		Implementation: "decoder-profile-v1", MaximumSourceDurationMS: 3_000,
		ObservationIntervalMS: 1_000, MaximumTimestampDriftMS: 1,
		MaximumObservations: 4, MinimumCoveredExposureMS: 1_003,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fillervisualsafety.Prepare(context.Background(), fillervisualsafety.SourceRequest{
		Authority: authority, Path: path,
	}, profile)
	if err != nil {
		t.Fatal(err)
	}
	return prepared, profile
}

func decoderExecutable(t *testing.T, arguments string, pts []string, exit int) string {
	t.Helper()
	var body strings.Builder
	body.WriteString("if [ \"$1\" = \"-version\" ]; then printf '%s\\n' 'ffmpeg version visual-test'; exit 0; fi\n")
	body.WriteString("printf '%s\\n' \"$@\" > \"")
	body.WriteString(arguments)
	body.WriteString("\"\n")
	for index, value := range pts {
		_, _ = fmt.Fprintf(&body, "printf '%%s\\n' '[Parsed_showinfo_1] n:%d pts:%d pts_time:%s fmt:rgb24 s:4x3' >&2\n", index, index, value)
		body.WriteString("dd if=/dev/zero bs=36 count=1 2>/dev/null\n")
	}
	if exit != 0 {
		_, _ = fmt.Fprintf(&body, "printf '%%s\\n' 'decode failed' >&2\nexit %d\n", exit)
	}
	return testkit.POSIXExecutable(t, "ffmpeg", body.String())
}
