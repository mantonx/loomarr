// Command filler-spoken-vctk-prepare creates a private, non-authorizing
// clean-control cohort from an already-acquired VCTK 0.92 release.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-spoken-vctk-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authority := flags.String("release-authority", "", "private VCTK 0.92 release authority JSON")
	root := flags.String("release-root", "", "already-acquired VCTK 0.92 release root")
	seed := flags.String("seed", "", "private deterministic selection seed")
	ffmpeg := flags.String("ffmpeg", "", "exact ffmpeg executable")
	ffprobe := flags.String("ffprobe", "", "exact ffprobe executable")
	policy := flags.String("policy-sha256", "", "private spoken-safety policy SHA-256")
	implementation := flags.String("implementation", "", "spoken-safety evaluator implementation identity")
	prepared := flags.String("prepared-at", "", "fixed RFC3339 preparation time")
	expected := flags.Int("expected-speakers", 0, "exact speaker count; certification preparation requires 100")
	maxInput := flags.Int64("max-input-bytes", 0, "maximum aggregate verified input bytes")
	maxOutput := flags.Int64("max-output-bytes", 0, "maximum aggregate prepared output bytes")
	maxWall := flags.Duration("max-wall-time", 0, "maximum preparation wall time")
	output := flags.String("output", "", "new private prepared-cohort directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	preparedAt, err := time.Parse(time.RFC3339, *prepared)
	if err != nil || *authority == "" || *root == "" || *seed == "" || *ffmpeg == "" || *ffprobe == "" ||
		*policy == "" || *implementation == "" || *expected != 100 || *maxInput <= 0 || *maxOutput <= 0 ||
		*maxWall <= 0 || *output == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-vctk-prepare: exact release, seed, tools, policy, implementation, time, 100 speakers, ceilings, and output are required")
		return 2
	}
	result, err := fillersafetycorpus.PrepareVCTK(context.Background(), fillersafetycorpus.PrepareVCTKConfig{
		ReleaseAuthorityPath: *authority, ReleaseRoot: *root, SeedPath: *seed,
		FFmpegPath: *ffmpeg, FFprobePath: *ffprobe, PolicySHA256: *policy, Implementation: *implementation,
		PreparedAt: preparedAt.UTC(), ExpectedSpeakers: *expected, MaximumInputBytes: *maxInput,
		MaximumOutputBytes: *maxOutput, MaximumWallTime: *maxWall, OutputDirectory: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-vctk-prepare:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-vctk-prepare: prepared %d speakers; cohort sha256 %s; owner map sha256 %s; %d input bytes; %d output bytes\n",
		result.Speakers, result.CohortSHA256, result.OwnerMapSHA256, result.InputBytes, result.OutputBytes)
	return 0
}
