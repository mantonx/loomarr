package mediatools_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestTranscodePreservesMeasuredGeometryCadenceAspectAndAVSkew(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mkv")
	output := filepath.Join(dir, "evidence.mp4")
	command := exec.Command(ffmpeg, "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30000/1001:duration=3",
		"-itsoffset", "0.120", "-f", "lavfi", "-i", "sine=frequency=700:sample_rate=48000:duration=2.88",
		"-map", "0:v:0", "-map", "1:a:0", "-vf", "setsar=1/1",
		"-c:v", "mpeg4", "-q:v", "2", "-c:a", "aac", "-y", source)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build source fixture: %v: %s", err, raw)
	}
	probe := filler.FFprobeNextTo(ffmpeg)
	input, err := probe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	recipe := mediatools.EvidenceDerivativeRecipe()
	if _, err := mediatools.Transcode(context.Background(), mediatools.TranscodeRequest{
		In: source, Out: output, DurationMs: input.DurationMs, HadAudio: true,
		InputProbe: &input, Profile: recipe.Profile(), Probe: probe,
	}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := probe(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != input.Width || got.Height != input.Height || got.Cadence != input.Cadence ||
		got.SampleAspect != input.SampleAspect || got.DisplayAspect != input.DisplayAspect {
		t.Fatalf("input probe = %+v; evidence probe = %+v", input, got)
	}
	qc, err := mediatools.VerifyDerivative(context.Background(), ffmpeg, output, got.DurationMs,
		recipe.KeyframeSeconds, !got.Silent, recipe.TargetLUFS)
	if err != nil {
		t.Fatal(err)
	}
	if !qc.FastStart || !qc.CompleteDecode || !qc.Seekable || !qc.Loudness.Available ||
		qc.MaxVideoKeyframeGapMs > 1_250 {
		t.Fatalf("derivative QC = %+v", qc)
	}
}

func TestTranscodeInterlacedFixturePreservesFieldOrderOrFailsClosed(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "interlaced-source.mkv")
	output := filepath.Join(dir, "evidence.mp4")
	command := exec.Command(ffmpeg, "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30000/1001:duration=2",
		"-vf", "tinterlace=mode=interleave_top",
		"-c:v", "libx264", "-flags", "+ildct+ilme", "-x264-params", "tff=1", "-an", "-y", source)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build interlaced source fixture: %v: %s", err, raw)
	}
	probe := filler.FFprobeNextTo(ffmpeg)
	input, err := probe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if input.FieldOrder == "" || input.FieldOrder == "progressive" {
		t.Fatalf("interlaced fixture field order = %q, want real interlaced observation", input.FieldOrder)
	}
	sourceIdentity := fixtureIdentity(t, source)
	recipe := mediatools.EvidenceDerivativeRecipe()
	err = transcodeFixture(t, source, output, input, recipe, probe)
	assertFixtureUnchanged(t, source, sourceIdentity)
	if err == nil {
		got, err := probe(context.Background(), output)
		if err != nil {
			t.Fatal(err)
		}
		if got.FieldOrder != input.FieldOrder {
			t.Fatalf("interlaced field order changed from %q to %q", input.FieldOrder, got.FieldOrder)
		}
		if _, err := mediatools.VerifyDerivative(context.Background(), ffmpeg, output, got.DurationMs,
			recipe.KeyframeSeconds, false, recipe.TargetLUFS); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !strings.Contains(err.Error(), "field order changed") {
		t.Fatalf("interlaced transcode error = %v, want specific field-order preservation hold", err)
	}
}

func TestTranscodeVariableFrameRateFixturePreservesFrameTiming(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "variable-frame-rate-source.mkv")
	output := filepath.Join(dir, "evidence.mp4")
	command := exec.Command(ffmpeg, "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-vf", "select='not(mod(n,5))+not(mod(n,7))'", "-fps_mode", "vfr", "-c:v", "ffv1", "-an", "-y", source)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build variable-frame-rate source fixture: %v: %s", err, raw)
	}
	sourceTiming := readFrameTiming(t, ffprobe, source)
	if !sourceTiming.variable() {
		t.Fatal("variable-frame-rate source fixture had constant frame timing")
	}
	probe := filler.FFprobeNextTo(ffmpeg)
	input, err := probe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	sourceIdentity := fixtureIdentity(t, source)
	recipe := mediatools.EvidenceDerivativeRecipe()
	err = transcodeFixture(t, source, output, input, recipe, probe)
	assertFixtureUnchanged(t, source, sourceIdentity)
	if err != nil {
		// Bind the hold to an independently observed output from this exact source
		// and recipe rather than to an FFmpeg-version-specific cadence literal.
		observedOutput := filepath.Join(dir, "observed-output.mp4")
		if _, observeErr := mediatools.Transcode(context.Background(), mediatools.TranscodeRequest{
			In: source, Out: observedOutput, DurationMs: input.DurationMs, HadAudio: false,
			Profile: recipe.Profile(), Probe: probe,
		}, nil); observeErr != nil {
			t.Fatalf("build output observation for variable-frame-rate preservation hold: %v", observeErr)
		}
		assertFixtureUnchanged(t, source, sourceIdentity)
		observed, observeErr := probe(context.Background(), observedOutput)
		if observeErr != nil {
			t.Fatalf("probe output observation for variable-frame-rate preservation hold: %v", observeErr)
		}
		if observed.Cadence == input.Cadence {
			t.Fatalf("variable-frame-rate preservation hold with unchanged observed cadence %q", observed.Cadence)
		}
		want := fmt.Sprintf("transcode variable-frame-rate-source.mkv: cadence changed from %q to %q", input.Cadence, observed.Cadence)
		if err.Error() != want {
			t.Fatalf("variable-frame-rate transcode error = %v, want the demonstrated cadence preservation hold", err)
		}
		return
	}
	outputTiming := readFrameTiming(t, ffprobe, output)
	if !outputTiming.variable() {
		t.Fatal("variable-frame-rate derivative had constant frame timing")
	}
	assertFrameTimingEqual(t, sourceTiming, outputTiming)
	got, err := probe(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cadence != input.Cadence {
		t.Fatalf("variable-frame-rate cadence changed from %q to %q", input.Cadence, got.Cadence)
	}
	if _, err := mediatools.VerifyDerivative(context.Background(), ffmpeg, output, got.DurationMs,
		recipe.KeyframeSeconds, false, recipe.TargetLUFS); err != nil {
		t.Fatal(err)
	}
}

func transcodeFixture(t *testing.T, source, output string, input mediatools.Probed, recipe mediatools.DerivativeRecipe, probe mediatools.Prober) error {
	t.Helper()
	_, err := mediatools.Transcode(context.Background(), mediatools.TranscodeRequest{
		In: source, Out: output, DurationMs: input.DurationMs, HadAudio: false,
		InputProbe: &input, Profile: recipe.Profile(), Probe: probe,
	}, nil)
	return err
}

type fixtureFingerprint struct {
	size   int64
	digest [sha256.Size]byte
}

func fixtureIdentity(t *testing.T, path string) fixtureFingerprint {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", filepath.Base(path), err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture %s: %v", filepath.Base(path), err)
	}
	return fixtureFingerprint{size: info.Size(), digest: sha256.Sum256(contents)}
}

func assertFixtureUnchanged(t *testing.T, path string, before fixtureFingerprint) {
	t.Helper()
	if after := fixtureIdentity(t, path); after != before {
		t.Fatalf("source fixture %s changed during transcode: before=%+v after=%+v", filepath.Base(path), before, after)
	}
}

type frameTiming struct {
	timeBase   string
	timestamps []int64
}

func (t frameTiming) variable() bool {
	if len(t.timestamps) < 3 {
		return false
	}
	firstDelta := t.timestamps[1] - t.timestamps[0]
	for i := 2; i < len(t.timestamps); i++ {
		if t.timestamps[i]-t.timestamps[i-1] != firstDelta {
			return true
		}
	}
	return false
}

func readFrameTiming(t *testing.T, ffprobe, path string) frameTiming {
	t.Helper()
	raw, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=time_base:frame=best_effort_timestamp", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("probe frame timestamps for %s: %v", filepath.Base(path), err)
	}
	var result struct {
		Streams []struct {
			TimeBase string `json:"time_base"`
		} `json:"streams"`
		Frames []struct {
			Timestamp json.Number `json:"best_effort_timestamp"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse frame timestamps for %s: %v", filepath.Base(path), err)
	}
	if len(result.Frames) < 3 {
		t.Fatalf("frame timestamps for %s = %d, want at least 3", filepath.Base(path), len(result.Frames))
	}
	if len(result.Streams) != 1 || result.Streams[0].TimeBase == "" {
		t.Fatalf("video time base for %s = %+v, want one observed time base", filepath.Base(path), result.Streams)
	}
	timing := frameTiming{timeBase: result.Streams[0].TimeBase, timestamps: make([]int64, 0, len(result.Frames))}
	for i, frame := range result.Frames {
		value, err := frame.Timestamp.Int64()
		if err != nil || (i > 0 && value <= timing.timestamps[i-1]) {
			t.Fatalf("frame timestamp %d = %q, want increasing integer time-base units: %v", i, frame.Timestamp, err)
		}
		timing.timestamps = append(timing.timestamps, value)
	}
	return timing
}

func assertFrameTimingEqual(t *testing.T, source, output frameTiming) {
	t.Helper()
	if len(source.timestamps) != len(output.timestamps) {
		t.Fatalf("frame timing shape changed: source=%+v output=%+v", source, output)
	}
	// ffprobe reports each best-effort timestamp as an integer in its container
	// time base. Compare those exact rationals, not rounded decimal seconds or a
	// tolerance, so differing but equivalent container time bases remain valid.
	sourceBase, ok := new(big.Rat).SetString(source.timeBase)
	if !ok {
		t.Fatalf("parse source time base %q", source.timeBase)
	}
	outputBase, ok := new(big.Rat).SetString(output.timeBase)
	if !ok {
		t.Fatalf("parse output time base %q", output.timeBase)
	}
	sourceOrigin, outputOrigin := source.timestamps[0], output.timestamps[0]
	for i := 1; i < len(source.timestamps); i++ {
		// Compare elapsed time from each stream's own first-frame origin. FFmpeg
		// may legitimately shift absolute origins while preserving cadence.
		sourceTimestamp := normalizedTimestamp(source.timestamps[i]-sourceOrigin, sourceBase)
		outputTimestamp := normalizedTimestamp(output.timestamps[i]-outputOrigin, outputBase)
		if sourceTimestamp.Cmp(outputTimestamp) != 0 {
			t.Fatalf("frame timestamp %d changed: source=%s (%d*%s) output=%s (%d*%s)", i, sourceTimestamp, source.timestamps[i], source.timeBase, outputTimestamp, output.timestamps[i], output.timeBase)
		}
	}
}

func normalizedTimestamp(timestamp int64, timeBase *big.Rat) *big.Rat {
	return new(big.Rat).Mul(big.NewRat(timestamp, 1), timeBase)
}
