// Command filler-spoken-safety-certify scores one pre-locked private spoken-
// safety challenge against an immutable source projection.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

type capabilities struct {
	publish func(fillerreview.TemporalSpokenSafetyCertificationConfig) (fillerreview.TemporalSpokenSafetyCertificationReport, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillerreview.PublishTemporalSpokenSafetyCertification}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-spoken-safety-certify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authority := flags.String("authority", "", "pre-locked private challenge authority")
	projection := flags.String("projection", "", "immutable private spoken-safety projection")
	scoredAtRaw := flags.String("scored-at", "", "fixed RFC3339 score time")
	output := flags.String("output", "", "new private certification report")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *authority == "" || *projection == "" || *scoredAtRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-certify: --authority, --projection, --scored-at, and --output are required")
		return 2
	}
	scoredAt, err := time.Parse(time.RFC3339Nano, *scoredAtRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-certify: --scored-at must be RFC3339")
		return 2
	}
	report, digest, err := capability.publish(fillerreview.TemporalSpokenSafetyCertificationConfig{AuthorityPath: *authority, SpokenSafetyReportPath: *projection, ScoredAt: scoredAt, OutputPath: *output})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filler-spoken-safety-certify: publish: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-safety-certify: %s; %d/%d positive families detected (one-sided exact lower %.4f), %d/%d clean false positives, %d coverage holds; sha256 %s in %s\n", report.CertificationStatus, report.DetectedPositiveSources, report.PositiveFamilies, report.SourceRecallExactLower95, report.CleanFalsePositiveSources, report.CleanSources, report.CoverageHolds, digest, *output)
	return 0
}
