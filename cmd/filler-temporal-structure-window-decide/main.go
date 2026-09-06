// Command filler-temporal-structure-window-decide projects two complete truth-blind family runs
// through the production reducer and publishes the long representation's shadow decision set.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

type capabilities struct {
	publish func(fillerreview.TemporalStructureWindowShadowDecisionSetConfig) (fillerreview.TemporalStructureShadowDecisionSet, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillerreview.PublishTemporalStructureWindowShadowDecisionSet}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-decide", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("window-set", "", "public prepared-window manifest JSON")
	first := flags.String("first-family", "", "first immutable truth-blind family result JSON")
	second := flags.String("second-family", "", "second immutable truth-blind family result JSON")
	decidedRaw := flags.String("decided-at", "", "fixed RFC3339 decision time")
	output := flags.String("out", "", "new immutable private window decision-set JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *manifest == "" || *first == "" || *second == "" || *decidedRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-decide: public window set, two family results, fixed decision time, and output are required")
		return 2
	}
	decidedAt, err := time.Parse(time.RFC3339Nano, *decidedRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-decide: --decided-at must be RFC3339")
		return 2
	}
	set, digest, err := capability.publish(fillerreview.TemporalStructureWindowShadowDecisionSetConfig{
		WindowSetManifestPath: *manifest, FirstFamilyPath: *first, SecondFamilyPath: *second,
		DecidedAt: decidedAt, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-decide:", err)
		return 1
	}
	confirmed := 0
	for _, item := range set.Cases {
		if item.Artifact.Decision.Status == fillerstructure.StatusConfirmed {
			confirmed++
		}
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-decide: %d/%d confirmed across %d model families; training=false production=false; sha256 %s; %s\n",
		confirmed, len(set.Cases), len(set.Families), digest, *output)
	return 0
}
