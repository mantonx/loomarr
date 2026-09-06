// Command filler-temporal-structure-window-certify joins two complete truth-blind family results
// to the private long-reel suite and publishes a non-authorizing certification artifact.
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
	publish func(fillerreview.TemporalStructureWindowCertificationConfig) (fillerreview.TemporalStructureWindowCertificationArtifact, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillerreview.PublishTemporalStructureWindowCertification}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-certify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	suite := flags.String("suite", "", "private long-reel certification suite JSON")
	manifest := flags.String("window-set", "", "public prepared-window manifest JSON")
	first := flags.String("first-family", "", "first immutable truth-blind family result JSON")
	second := flags.String("second-family", "", "second immutable truth-blind family result JSON")
	certifiedRaw := flags.String("certified-at", "", "fixed RFC3339 certification time")
	output := flags.String("out", "", "new immutable private certification JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *suite == "" || *manifest == "" || *first == "" || *second == "" || *certifiedRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-certify: private suite, public window set, two family results, fixed certification time, and output are required")
		return 2
	}
	certifiedAt, err := time.Parse(time.RFC3339Nano, *certifiedRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-certify: --certified-at must be RFC3339")
		return 2
	}
	artifact, digest, err := capability.publish(fillerreview.TemporalStructureWindowCertificationConfig{
		SuitePath: *suite, WindowSetManifestPath: *manifest, FirstFamilyPath: *first,
		SecondFamilyPath: *second, CertifiedAt: certifiedAt, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-certify:", err)
		return 1
	}
	report := artifact.Report
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-certify: %s; %d/%d decided, %d wrong, %d held across %d model families; training=false automaticMaterialization=false; sha256 %s; %s\n",
		report.Status, report.DecidedCases, report.Cases, report.WrongCases, report.HeldCases, len(artifact.Families), digest, *output)
	return 0
}
