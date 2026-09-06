// Command filler-temporal-structure-prepare materializes a provenance-grounded,
// construction-authoritative temporal challenge with separate public and
// coordinator-private roots.
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
	flags := flag.NewFlagSet("filler-temporal-structure-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authoring := flags.String("authoring", "", "coordinator-private construction authority JSON")
	planReceipt := flags.String("plan-receipt", "", "coordinator-private validated holdout plan receipt JSON")
	sourceRoot := flags.String("source-root", "", "root containing authority-bound source media")
	challengeID := flags.String("challenge-id", "", "opaque challenge identity visible to assessors")
	seed := flags.String("seed", "", "private deterministic blinding seed")
	seedFile := flags.String("seed-file", "", "file containing the private deterministic blinding seed")
	generatedText := flags.String("generated-at", "", "fixed RFC3339 generation time")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "FFmpeg executable")
	ffprobe := flags.String("ffprobe", "ffprobe", "FFprobe executable")
	output := flags.String("out", "", "new challenge directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	generatedAt, err := time.Parse(time.RFC3339, *generatedText)
	if err != nil || *authoring == "" || *planReceipt == "" || *sourceRoot == "" || *challengeID == "" || (*seed == "") == (*seedFile == "") || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-prepare: authoring, plan receipt, source root, challenge id, private seed, fixed generation time, and output are required")
		return 2
	}
	seedValue := *seed
	if *seedFile != "" {
		seedValue, err = fillerreview.LoadPrivateSeed(*seedFile)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-prepare: read private seed file")
			return 2
		}
	}
	media, err := fillerreview.NewFFmpegTemporalStructureMedia(ctx, *ffmpeg, *ffprobe)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-prepare:", err)
		return 1
	}
	result, err := fillerreview.BuildTemporalStructureChallenge(ctx, fillerreview.TemporalStructureChallengeConfig{
		AuthoringPath: *authoring, PlanReceiptPath: *planReceipt, SourceRoot: *sourceRoot, OutputDir: *output, ChallengeID: *challengeID,
		Seed: seedValue, GeneratedAt: generatedAt.UTC(), Media: media,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-prepare:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-prepare: wrote %d blinded cases; public manifest sha256 %s; private authority sha256 %s\n", result.Cases, result.PublicManifestSHA256, result.AuthoritySHA256)
	return 0
}
