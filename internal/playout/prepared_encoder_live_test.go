//go:build ffmpeg

package playout_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/prepared"
	"github.com/loomarr/loomarr/internal/testkit"
)

// This is the tagged proof that the shared live-encoder policy also produces a complete prepared
// publication on the hardware this host actually detected. It skips software-only machines.
func TestLivePreparedPackagerUsesDetectedHardware(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if configured := os.Getenv("FFMPEG_PATH"); configured != "" {
		bin, err = configured, nil
	}
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	capability := playout.DetectObserved(ctx, bin, playout.DefaultProfile(), "", nil)
	if capability.Chosen == playout.EncoderSoftware {
		t.Skip("no working hardware encoder")
	}
	source := filepath.Join(t.TempDir(), "source.mp4")
	generate := exec.CommandContext(ctx, bin,
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "testsrc2=duration=3:size=320x180:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=3",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", source,
	)
	if output, generateErr := generate.CombinedOutput(); generateErr != nil {
		t.Fatalf("generate source: %v\n%s", generateErr, output)
	}

	library, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rendition := playout.CanonicalPreparedRendition(playout.TierEfficient)
	rendition.Width, rendition.Height = 320, 180
	rendition.VideoBitrateKbps = 500
	packager := prepared.NewFFmpegPackager(bin, func(r prepared.RenditionContract) (prepared.VideoPlan, error) {
		return playout.PreparedVideoArgs(capability.Chosen, r)
	})
	preparer := prepared.NewPreparer(prepared.PreparerDependencies{
		Library: library, Packager: packager,
		Access: &testkit.PreparedSourceAccess{Input: prepared.LocalInput(source)},
	})
	publication, err := preparer.Prepare(ctx, prepared.Request{
		Source: prepared.Source{
			ItemID: "hardware-test-item", SourceID: "hardware-test-source", Revision: "generated-v1",
		},
		Rendition: rendition,
	})
	if err != nil {
		t.Fatalf("prepare with %s: %v", capability.Chosen, err)
	}
	manifest, ok, err := library.Open(publication.Key, prepared.MediaManifestName)
	if err != nil || !ok {
		t.Fatalf("open manifest = ok %v err %v", ok, err)
	}
	body := make([]byte, 8192)
	n, readErr := manifest.Content.Read(body)
	_ = manifest.Content.Close()
	if readErr != nil && n == 0 {
		t.Fatal(readErr)
	}
	if text := string(body[:n]); !strings.Contains(text, "#EXT-X-MAP") || !strings.Contains(text, ".m4s") {
		t.Fatalf("hardware publication is not fMP4 HLS:\n%s", text)
	}
}
