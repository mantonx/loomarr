// Command filler-temporal-transition-authority measures immutable edge facts
// for every exact temporal evidence case before holdout selection.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-temporal-transition-authority", flag.ContinueOnError)
	flags.SetOutput(stderr)
	evidence := flags.String("evidence", "", "public evidence manifest JSON")
	privateMap := flags.String("evidence-map", "", "private evidence map JSON")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "exact FFmpeg executable")
	generatedText := flags.String("generated-at", "", "fixed RFC3339 generation time")
	timeout := flags.Duration("case-timeout", 2*time.Minute, "per-case measurement timeout")
	output := flags.String("out", "", "new private authority directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	generatedAt, err := time.Parse(time.RFC3339, *generatedText)
	if err != nil || *evidence == "" || *privateMap == "" || *ffmpeg == "" || *timeout <= 0 || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-transition-authority: evidence, private map, FFmpeg, fixed generation time, positive case timeout, and output are required")
		return 2
	}
	media, err := fillerreview.NewExecTemporalTransitionEvidenceMedia(ctx, *ffmpeg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-transition-authority:", err)
		return 1
	}
	result, err := fillerreview.BuildTemporalTransitionAuthority(ctx, fillerreview.TemporalTransitionAuthorityConfig{
		EvidenceManifestPath: *evidence, EvidencePrivateMapPath: *privateMap,
		GeneratedAt: generatedAt.UTC(), PerCaseTimeout: *timeout, OutputDir: *output, Media: media,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-transition-authority:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-transition-authority: measured %d cases; authority sha256 %s; training false; production admission false\n", result.Cases, result.AuthoritySHA256)
	return 0
}
