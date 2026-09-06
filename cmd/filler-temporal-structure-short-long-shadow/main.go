// Command filler-temporal-structure-short-long-shadow compares the independently reduced
// complete-video and overlapping-window representations against one passing window certificate.
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
	publish func(fillerreview.TemporalStructureShortLongShadowConfig) (fillerreview.TemporalStructureShortLongShadowArtifact, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillerreview.PublishTemporalStructureShortLongShadow}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-short-long-shadow", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("window-set", "", "public prepared-window manifest JSON")
	certificate := flags.String("window-certificate", "", "passing immutable private window certification JSON")
	complete := flags.String("complete-decisions", "", "immutable complete-video decision-set JSON")
	windows := flags.String("window-decisions", "", "immutable overlapping-window decision-set JSON")
	comparedRaw := flags.String("compared-at", "", "fixed RFC3339 comparison time")
	output := flags.String("out", "", "new immutable private short-versus-long shadow JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *manifest == "" || *certificate == "" || *complete == "" || *windows == "" || *comparedRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-short-long-shadow: public window set, passing window certificate, both decision sets, fixed comparison time, and output are required")
		return 2
	}
	comparedAt, err := time.Parse(time.RFC3339Nano, *comparedRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-short-long-shadow: --compared-at must be RFC3339")
		return 2
	}
	artifact, digest, err := capability.publish(fillerreview.TemporalStructureShortLongShadowConfig{
		WindowSetManifestPath: *manifest, WindowCertificationPath: *certificate,
		CompleteDecisionSetPath: *complete, WindowDecisionSetPath: *windows,
		ComparedAt: comparedAt, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-short-long-shadow:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-short-long-shadow: %s; %d/%d cases agree; training=false production=false materialization=false; next=%s; sha256 %s; %s\n",
		artifact.Report.Status, artifact.Report.PassedCases, len(artifact.Report.ExpectedAliases), artifact.Report.NextAction, digest, *output)
	return 0
}
