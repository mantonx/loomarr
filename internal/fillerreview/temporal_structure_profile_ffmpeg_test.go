//go:build ffmpeg

package fillerreview

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTemporalStructureFFmpegMeasuresNormalizedPartsAndConcat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	media, err := NewFFmpegTemporalStructureMedia(ctx, "ffmpeg", "ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	if output, err := exec.CommandContext(ctx, media.Identity().FFmpeg.Path,
		"-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x240:r=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-threads", "1", "-c:a", "aac", source).CombinedOutput(); err != nil {
		t.Fatalf("synthetic source: %v: %s", err, output)
	}
	for _, parts := range []int{1, 2} {
		segments := make([]TemporalStructureRenderSegment, parts)
		for i := range segments {
			segments[i] = TemporalStructureRenderSegment{SourcePath: source, StartMS: int64(i) * 1000, DurationMS: 1000}
		}
		output := filepath.Join(t.TempDir(), "render.mp4")
		rendered, err := media.Render(ctx, segments, output)
		if err != nil {
			t.Fatalf("render %d parts: %v", parts, err)
		}
		if len(rendered.Parts) != parts || rendered.Video.Profile != temporalStructureTestProfile() || rendered.Video.Width != 960 || rendered.Video.Height != 720 {
			t.Fatalf("unexpected measured normalized result: %+v", rendered)
		}
	}
}
