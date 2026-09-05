// Command filler-corpus-quarantine-inspect proves the local technical state
// and prior-holdout separation of quarantine downloads. It never modifies,
// uploads, labels, promotes, or grants downstream use of media.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerquarantine"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-quarantine-inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "exact frozen schema-v4 inventory")
	ledger := flags.String("ledger", "", "schema-v2 quarantine download ledger")
	downloadRoot := flags.String("download-root", "", "root containing quarantine downloads")
	priorPublic := flags.String("prior-public", "", "prior holdout public manifest")
	priorAuthority := flags.String("prior-authority", "", "prior holdout private authority")
	priorSourceRoot := flags.String("prior-source-root", "", "root beneath every prior authority source path")
	priorCases := flags.Int("prior-cases", 0, "exact prior holdout case count")
	maxMediaWallTime := flags.Duration("max-media-wall-time", 0, "positive ceiling for hashing, media tools, and fingerprint comparison")
	ffmpeg := flags.String("ffmpeg", "", "ffmpeg executable; ffprobe is resolved next to it")
	output := flags.String("output", "", "new immutable quarantine inspection JSON")
	generatedText := flags.String("generated-at", "", "fixed RFC3339 inspection time")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	generatedAt, err := time.Parse(time.RFC3339, *generatedText)
	if err != nil || *inventory == "" || *ledger == "" || *downloadRoot == "" || *priorPublic == "" || *priorAuthority == "" || *priorSourceRoot == "" || *priorCases <= 0 || maxMediaWallTime.Milliseconds() <= 0 || *ffmpeg == "" || *output == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-quarantine-inspect: inventory, ledger, download root, prior public/private authority, prior source root/count, positive media wall-time ceiling, ffmpeg, output, and fixed generated-at are required")
		return 2
	}
	ctx := context.Background()
	media, err := newExecMedia(ctx, *ffmpeg)
	if err != nil {
		return fail(stderr, err)
	}
	report, err := fillerquarantine.Inspect(ctx, fillerquarantine.Config{
		InventoryPath: *inventory, DownloadLedgerPath: *ledger, DownloadRoot: *downloadRoot,
		PriorPublicManifestPath: *priorPublic, PriorAuthorityPath: *priorAuthority, PriorSourceRoot: *priorSourceRoot,
		ExpectedPriorCases: *priorCases, MaxMediaWallTime: *maxMediaWallTime, GeneratedAt: generatedAt, Media: media,
	})
	if err != nil {
		return fail(stderr, err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fail(stderr, err)
	}
	if err := publish(*output, append(data, '\n')); err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-quarantine-inspect: %d eligible for rights review, %d held; %d prior sources checked\n", report.Summary.EligibleForRightsReview, report.Summary.Held, report.Summary.PriorSources)
	return 0
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "filler-corpus-quarantine-inspect:", err)
	return 1
}
