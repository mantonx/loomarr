// Command filler-temporal-structure-window-preflight verifies the sealed long-reel inputs and
// publishes the exact, provider-free request and production-duration envelope before paid work.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

type capabilities struct {
	publish func(fillerreview.TemporalStructureWindowPreflightConfig) (fillerreview.TemporalStructureWindowPreflight, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillerreview.PublishTemporalStructureWindowPreflight}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-preflight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("window-set", "", "complete public prepared-window manifest")
	suite := flags.String("suite", "", "private pre-model certification suite")
	shortCeiling := flags.Int64("short-source-ceiling-ms", 0, "maximum source duration covered by the complete-video production slice")
	longCeiling := flags.Int64("long-source-ceiling-ms", 0, "intended maximum source duration for the first windowed production slice")
	output := flags.String("out", "", "new immutable private preflight JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *manifest == "" || *suite == "" || *shortCeiling <= 0 || *longCeiling <= 0 || *output == "" || capability.publish == nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-preflight: complete window set, private suite, both production duration ceilings, and output are required")
		return 2
	}
	report, fileSHA, err := capability.publish(fillerreview.TemporalStructureWindowPreflightConfig{
		WindowSetManifestPath: *manifest, SuitePath: *suite,
		ShortSourceCeilingMS: *shortCeiling, IntendedLongSourceCeilingMS: *longCeiling, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-preflight:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-preflight: %s; %d cases, %d windows/family + %d complete videos/family = %d serial provider requests; observed source=%d..%dms windows=%d..%d bytes/window=%d..%d; lower-edge=%d/%d upper-edge=%d/%d; training=false production=false materialization=false; next=%s; sha256 %s; %s\n",
		report.Status, report.Cases, report.WindowRequestsPerFamily, report.CompleteVideoRequestsPerFamily,
		report.TotalProviderRequests, report.MinimumObservedSourceDurationMS, report.MaximumObservedSourceDurationMS,
		report.MinimumObservedWindowsPerSource, report.MaximumObservedWindowsPerSource,
		report.MinimumObservedWindowBytes, report.MaximumObservedWindowBytes,
		report.LowerEnvelopeEdgeCases, report.MinimumRequiredEnvelopeEdgeCases,
		report.UpperEnvelopeEdgeCases, report.MinimumRequiredEnvelopeEdgeCases,
		report.NextAction, fileSHA, *output)
	if !report.ReadyForPaidCertification {
		return 1
	}
	return 0
}
