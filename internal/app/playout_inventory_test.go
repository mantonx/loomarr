package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

// Configuring a large lineup must remain database/control-plane work. Only a viewer Attach owns
// authority to start a live parent process; boot, codec backfill, telemetry, and shutdown must not
// turn Channel inventory into speculative FFmpeg work.
func TestBuildSixtyFourConfiguredChannelsWithoutViewersStartsNoMediaProcesses(t *testing.T) {
	t.Setenv("API_TOKEN", "idle-lineup-token")
	t.Setenv("PLAYOUT_BACKEND", schedule.PlayoutBackendInternal)
	t.Setenv("CHANNEL_RECONCILE_EVERY", "9999h")

	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	marker := ffmpeg + ".invoked"
	stub := "#!/bin/sh\nprintf invoked > \"$0.invoked\"\nexit 99\n"
	if err := os.WriteFile(ffmpeg, []byte(stub), 0o755); err != nil { //nolint:gosec // executable test double
		t.Fatal(err)
	}
	t.Setenv("PLAYOUT_FFMPEG_PATH", ffmpeg)

	ctx, cancel := context.WithCancel(context.Background())
	st := testkit.MigratedSQLiteStore(t)
	application, err := Build(ctx, st, slog.New(slog.DiscardHandler), Overrides{})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	// Seed after composition so this test isolates inventory growth from the separate one-time
	// codec migration pass. Saving a Channel is the real supported configuration boundary.
	for i := range 64 {
		_, err = st.SaveChannel(ctx, store.Channel{Channel: schedule.Channel{
			ID:       fmt.Sprintf("configured-%02d", i),
			Name:     fmt.Sprintf("Configured %02d", i),
			Number:   i + 1,
			Strategy: schedule.Sequential,
			Status:   schedule.StatusLive,
		}})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
	}
	cancel()
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("64 configured Channels with no viewers invoked FFmpeg: marker error = %v", err)
	}
}
