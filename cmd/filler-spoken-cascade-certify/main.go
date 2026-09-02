// Command filler-spoken-cascade-certify scores one pre-authored private
// authority against an exhaustive label-blind durable cascade manifest.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

type capabilities struct {
	publish func(fillersafetycert.Config) (fillersafetycert.Report, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillersafetycert.Publish}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-spoken-cascade-certify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authority := flags.String("authority", "", "pre-authored private cascade authority")
	results := flags.String("results", "", "exhaustive label-blind cascade result manifest")
	scoredAtRaw := flags.String("scored-at", "", "fixed RFC3339 score time")
	output := flags.String("output", "", "new private certification report")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *authority == "" || *results == "" || *scoredAtRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-cascade-certify: --authority, --results, --scored-at, and --output are required")
		return 2
	}
	scoredAt, err := time.Parse(time.RFC3339Nano, *scoredAtRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-cascade-certify: --scored-at must be RFC3339")
		return 2
	}
	report, digest, err := capability.publish(fillersafetycert.Config{
		AuthorityPath: *authority, ResultsPath: *results, ScoredAt: scoredAt, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filler-spoken-cascade-certify: publish: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout,
		"filler-spoken-cascade-certify: %s; %d/%d positive families detected (one-sided exact lower %.4f), %d/%d clean false positives, %d coverage holds; sha256 %s in %s\n",
		report.CertificationStatus, report.DetectedPositiveSources, report.PositiveFamilies,
		report.SourceRecallExactLower95, report.CleanFalsePositiveSources, report.CleanSources,
		report.CoverageHolds, digest, *output)
	return 0
}
