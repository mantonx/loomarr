// Command filler-temporal-structure-window-suite measures pre-model wordless and motion traits
// and publishes the private, immutable long-reel certification suite.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-suite", flag.ContinueOnError)
	flags.SetOutput(stderr)
	windowSet := flags.String("window-set", "", "verified public prepared-window manifest")
	windowAuthority := flags.String("window-authority", "", "private prepared-window authority")
	corpus := flags.String("corpus", "", "verified public rendered-corpus manifest")
	corpusAuthority := flags.String("corpus-authority", "", "private rendered-corpus authority")
	authoring := flags.String("authoring", "", "locked holdout authoring")
	receipt := flags.String("receipt", "", "locked holdout receipt")
	evidence := flags.String("evidence", "", "locked temporal evidence manifest")
	evidenceMap := flags.String("evidence-map", "", "private temporal evidence map")
	measuredText := flags.String("measured-at", "", "fixed RFC3339 measurement time")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "ffmpeg executable")
	output := flags.String("out", "", "new atomic private suite directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	measuredAt, err := time.Parse(time.RFC3339, *measuredText)
	if err != nil || *windowSet == "" || *windowAuthority == "" || *corpus == "" || *corpusAuthority == "" ||
		*authoring == "" || *receipt == "" || *evidence == "" || *evidenceMap == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-suite: prepared windows, rendered corpus, locked holdout/evidence authorities, fixed measurement time, and output are required")
		return 2
	}
	motion, err := fillerreview.NewFFmpegTemporalStructureWindowMotionMeasurer(context.Background(), *ffmpeg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-suite: motion:", err)
		return 1
	}
	result, err := fillerreview.BuildTemporalStructureWindowCertificationSuite(context.Background(), fillerreview.TemporalStructureWindowSuiteConfig{
		WindowSetManifestPath: *windowSet, WindowSetAuthorityPath: *windowAuthority,
		CorpusManifestPath: *corpus, CorpusAuthorityPath: *corpusAuthority,
		HoldoutAuthoringPath: *authoring, HoldoutReceiptPath: *receipt,
		EvidenceManifestPath: *evidence, EvidencePrivateMapPath: *evidenceMap,
		MeasuredAt: measuredAt.UTC(), OutputDir: *output, Motion: motion,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-suite:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-suite: prepared %d cases; wordless=%d high-motion=%d windows=%d; evidence sha256 %s; suite sha256 %s; training false; production false\n",
		result.Cases, result.WordlessCases, result.HighMotionCases, result.WindowsMeasured, result.EvidenceSHA256, result.SuiteSHA256)
	return 0
}
