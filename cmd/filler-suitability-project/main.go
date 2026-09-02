// Command filler-suitability-project projects rendered-case suitability
// evidence back to private source authority and propagates source quarantine
// to every derivative case.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-suitability-project", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("manifest", "", "verified public structure-challenge manifest")
	authority := flags.String("authority", "", "private structure construction authority")
	comparison := flags.String("comparison", "", "locked two-family suitability comparison")
	first := flags.String("first", "", "first immutable suitability result named by the comparison")
	second := flags.String("second", "", "second immutable suitability result named by the comparison")
	expectedCases := flags.Int("expected-cases", 36, "exact case count")
	projectedText := flags.String("projected-at", "", "fixed RFC3339 projection time")
	output := flags.String("out", "", "new immutable private projection JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	projectedAt, err := time.Parse(time.RFC3339, *projectedText)
	if err != nil || *manifest == "" || *authority == "" || *comparison == "" || *first == "" || *second == "" || *expectedCases <= 0 || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-suitability-project: manifest, authority, comparison, two results, positive expected cases, fixed projection time, and output are required")
		return 2
	}
	report, digest, err := fillerreview.PublishTemporalSuitabilityProjection(fillerreview.TemporalSuitabilityProjectionConfig{
		PublicManifestPath: *manifest, StructureAuthorityPath: *authority, SuitabilityComparisonPath: *comparison,
		FirstResultPath: *first, SecondResultPath: *second, ExpectedCases: *expectedCases,
		ProjectedAt: projectedAt.UTC(), OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-suitability-project:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-suitability-project: %d cases from %d sources; prohibited=%d operational-hold=%d coverage-hold=%d candidate-no-signal=%d; trainingAllowed=%t ingestionAllowed=%t productionAdmissionAllowed=%t; sha256 %s; %s\n", report.Cases, report.Sources, report.ProhibitedSources, report.OperationalHoldSources, report.CoverageHoldSources, report.CandidateNoSignalSources, report.TrainingAllowed, report.IngestionAllowed, report.ProductionAdmissionAllowed, digest, *output)
	return 0
}
