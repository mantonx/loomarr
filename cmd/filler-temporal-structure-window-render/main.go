// Command filler-temporal-structure-window-render materializes the fixed private long-reel corpus
// without calling a model or granting training or production authority.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-render", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plan := flags.String("plan", "", "fixed private window-corpus plan JSON")
	authoring := flags.String("authoring", "", "locked 60-case private authoring JSON")
	receipt := flags.String("receipt", "", "locked 60-case private receipt JSON")
	sourceRoot := flags.String("source-root", "", "root containing every authority-bound source")
	seed := flags.String("seed", "", "private deterministic corpus seed")
	renderedText := flags.String("rendered-at", "", "fixed RFC3339 rendering time")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "ffmpeg executable")
	ffprobe := flags.String("ffprobe", "ffprobe", "ffprobe executable")
	output := flags.String("out", "", "new atomic window-corpus media directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	renderedAt, err := time.Parse(time.RFC3339, *renderedText)
	if err != nil || *plan == "" || *authoring == "" || *receipt == "" || *sourceRoot == "" || *seed == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-render: plan, locked authority, source root, private seed, fixed rendering time, and output are required")
		return 2
	}
	media, err := fillerreview.NewFFmpegTemporalStructureMedia(context.Background(), *ffmpeg, *ffprobe)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-render: media:", err)
		return 1
	}
	result, err := fillerreview.BuildTemporalStructureWindowCorpusMedia(context.Background(), fillerreview.TemporalStructureWindowMediaConfig{
		PlanPath: *plan, HoldoutAuthoringPath: *authoring, HoldoutReceiptPath: *receipt,
		SourceRoot: *sourceRoot, Seed: *seed, RenderedAt: renderedAt.UTC(), OutputDir: *output, Media: media,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-render:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-render: rendered %d cases; public sha256 %s; private sha256 %s; training false; production false\n", result.Cases, result.PublicManifestSHA256, result.PrivateAuthoritySHA256)
	return 0
}
