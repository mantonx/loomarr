//go:build ffmpeg

package testkit

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// FillerQuarantineFixtures are two distinct synthetic six-second sources for
// exercising the complete quarantine inspection adapter without third-party
// media or network access.
type FillerQuarantineFixtures struct {
	Candidate string
	Prior     string
}

func FillerQuarantineMedia(t testing.TB, dir string) FillerQuarantineFixtures {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("ffmpeg build-tag fixture requires ffmpeg")
	}
	fixtures := FillerQuarantineFixtures{
		Candidate: filepath.Join(dir, "quarantine-candidate.mp4"),
		Prior:     filepath.Join(dir, "quarantine-prior.mp4"),
	}
	render := func(video, audio, output string) {
		runFixtureCommand(t, ffmpeg, []string{
			"-nostdin", "-v", "error",
			"-f", "lavfi", "-i", video,
			"-f", "lavfi", "-i", audio,
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "96k", "-ar", "48000", "-ac", "2",
			"-movflags", "+faststart", "-y", output,
		})
	}
	render("testsrc2=size=320x180:rate=30:duration=6", "sine=frequency=440:sample_rate=48000:duration=6", fixtures.Candidate)
	render("testsrc=size=320x180:rate=30:duration=6", "sine=frequency=880:sample_rate=48000:duration=6", fixtures.Prior)
	return fixtures
}
