// Command filler-temporal-structure-window-plan builds the private, deterministic long-reel
// seam corpus plan from the locked 60-case construction authorities.
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
	flags := flag.NewFlagSet("filler-temporal-structure-window-plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authoring := flags.String("authoring", "", "locked 60-case private authoring JSON")
	receipt := flags.String("receipt", "", "locked 60-case private receipt JSON")
	seed := flags.String("seed", "", "private deterministic construction seed")
	plannedText := flags.String("planned-at", "", "fixed RFC3339 planning time")
	output := flags.String("out", "", "new private window-corpus plan directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	plannedAt, err := time.Parse(time.RFC3339, *plannedText)
	if err != nil || *authoring == "" || *receipt == "" || *seed == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-plan: locked authoring, receipt, private seed, fixed planning time, and output are required")
		return 2
	}
	result, err := fillerreview.BuildTemporalStructureWindowCorpusPlan(fillerreview.TemporalStructureWindowCorpusConfig{
		HoldoutAuthoringPath: *authoring, HoldoutReceiptPath: *receipt,
		Seed: *seed, PlannedAt: plannedAt.UTC(), OutputDir: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-plan:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-plan: planned %d cases; plan sha256 %s; file sha256 %s; training false; production false\n", result.Cases, result.PlanSHA256, result.PlanFileSHA256)
	return 0
}
