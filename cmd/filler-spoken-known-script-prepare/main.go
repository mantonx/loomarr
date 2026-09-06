// Command filler-spoken-known-script-prepare creates a private,
// non-authorizing positive cohort from already acquired, consented real speech.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-spoken-known-script-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authority := flags.String("authority", "", "private owner-authored known-script authority JSON")
	root := flags.String("source-root", "", "private already-acquired recording root")
	seed := flags.String("seed", "", "private deterministic alias seed")
	ffmpeg := flags.String("ffmpeg", "", "exact ffmpeg executable")
	ffprobe := flags.String("ffprobe", "", "exact ffprobe executable")
	prepared := flags.String("prepared-at", "", "fixed RFC3339 preparation time")
	expected := flags.Int("expected-speakers", 0, "exact real-speaker count; certification requires at least 59")
	maxInput := flags.Int64("max-input-bytes", 0, "maximum aggregate verified input bytes")
	maxOutput := flags.Int64("max-output-bytes", 0, "maximum aggregate prepared output bytes")
	maxWall := flags.Duration("max-wall-time", 0, "maximum preparation wall time")
	output := flags.String("output", "", "new private prepared-cohort directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	preparedAt, err := time.Parse(time.RFC3339, *prepared)
	if err != nil || *authority == "" || *root == "" || *seed == "" || *ffmpeg == "" || *ffprobe == "" ||
		*expected < fillersafetycert.MinimumPositiveFamilies || *maxInput <= 0 || *maxOutput <= 0 ||
		*maxWall <= 0 || *output == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-known-script-prepare: exact authority, root, seed, tools, time, at least 59 speakers, ceilings, and output are required")
		return 2
	}
	result, err := fillersafetycorpus.PrepareKnownScript(context.Background(), fillersafetycorpus.PrepareKnownScriptConfig{
		AuthorityPath: *authority, SourceRoot: *root, SeedPath: *seed,
		FFmpegPath: *ffmpeg, FFprobePath: *ffprobe, PreparedAt: preparedAt.UTC(),
		ExpectedSpeakers: *expected, MaximumInputBytes: *maxInput, MaximumOutputBytes: *maxOutput,
		MaximumWallTime: *maxWall, OutputDirectory: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-known-script-prepare:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-known-script-prepare: prepared %d speakers; cohort sha256 %s; owner map sha256 %s; %d input bytes; %d output bytes\n",
		result.Speakers, result.CohortSHA256, result.OwnerMapSHA256, result.InputBytes, result.OutputBytes)
	return 0
}
