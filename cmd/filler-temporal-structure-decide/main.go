// Command filler-temporal-structure-decide publishes truth-blind consensus
// decisions from at least two independently locked model families.
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
	publish func(fillerreview.TemporalStructureDecisionConfig) (fillerreview.TemporalStructureDecisionReport, string, error)
}

type assessmentPaths []string

func (paths *assessmentPaths) String() string { return fmt.Sprint([]string(*paths)) }

func (paths *assessmentPaths) Set(value string) error {
	if value == "" {
		return fmt.Errorf("assessment path cannot be empty")
	}
	*paths = append(*paths, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillerreview.PublishTemporalStructureDecisions}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-decide", flag.ContinueOnError)
	flags.SetOutput(stderr)
	public := flags.String("public", "", "public temporal-structure manifest JSON")
	authoritySHA := flags.String("authority-sha256", "", "expected private construction-authority SHA-256; the file is never opened")
	var assessments assessmentPaths
	flags.Var(&assessments, "assessment", "locked assessment JSON; repeat for each independent model family")
	cases := flags.Int("cases", 0, "exact expected case count")
	decidedRaw := flags.String("decided-at", "", "fixed RFC3339 decision time")
	output := flags.String("out", "", "new immutable private decision JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *public == "" || *authoritySHA == "" || len(assessments) < 2 || *cases <= 0 || *decidedRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-decide: public challenge, private-authority digest, at least two --assessment values, positive exact case count, fixed decision time, and output are required")
		return 2
	}
	decidedAt, err := time.Parse(time.RFC3339Nano, *decidedRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-decide: --decided-at must be RFC3339")
		return 2
	}
	report, digest, err := capability.publish(fillerreview.TemporalStructureDecisionConfig{
		PublicManifestPath: *public, PrivateAuthoritySHA256: *authoritySHA,
		AssessmentPaths: assessments, ExpectedCases: *cases, DecidedAt: decidedAt, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-decide:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-decide: %d/%d cases confirmed across %d independent model families; %d held; productionAdmissionAllowed=%t; sha256 %s; %s\n", report.ConfirmedCases, report.Cases, report.IndependentModelFamilies, report.HeldCases, report.ProductionAdmissionAllowed, digest, *output)
	return 0
}
