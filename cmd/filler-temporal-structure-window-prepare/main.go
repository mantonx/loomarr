// Command filler-temporal-structure-window-prepare packages a rendered long-reel corpus through
// the production complete-coverage window preparer without calling an assessor.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerreview"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("corpus", "", "verified public rendered-corpus manifest")
	authority := flags.String("authority", "", "private rendered-corpus authority")
	preparedText := flags.String("prepared-at", "", "fixed RFC3339 preparation time")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "ffmpeg executable")
	output := flags.String("out", "", "new atomic window-set package directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	preparedAt, err := time.Parse(time.RFC3339, *preparedText)
	if err != nil || *manifest == "" || *authority == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-prepare: corpus manifest, private authority, fixed preparation time, and output are required")
		return 2
	}
	result, err := fillerreview.BuildTemporalStructureWindowSet(context.Background(), fillerreview.TemporalStructureWindowSetConfig{
		CorpusManifestPath: *manifest, CorpusAuthorityPath: *authority, PreparedAt: preparedAt.UTC(), OutputDir: *output,
		NewPreparer: func(root string) (filler.StructureAssessmentWindowMediaPreparer, error) {
			return filler.NewFFmpegStructureAssessmentMediaPreparer(root, root, *ffmpeg)
		},
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-prepare:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-prepare: prepared %d cases and %d windows; public sha256 %s; private sha256 %s; training false; production false\n", result.Cases, result.Windows, result.PublicManifestSHA256, result.PrivateAuthoritySHA256)
	return 0
}
