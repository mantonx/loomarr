package mediatools_test

import (
	"context"
	"os/exec"
	"path/filepath"
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
