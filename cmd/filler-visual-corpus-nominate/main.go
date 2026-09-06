// Command filler-visual-corpus-nominate prepares and locks the minimum human
// review between rights-approved materialization and a visual corpus draft.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const (
	maximumNominationInputBytes = int64(16 << 20)
	nominationWorksheetFilename = "worksheet.json"
	nominationReviewFilename    = "review.csv"
	nominationBoardFilename     = "review.html"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "filler-visual-corpus-nominate: prepare or lock subcommand is required")
		return 2
	}
	var err error
	switch args[0] {
	case "prepare":
		err = runPrepare(ctx, args[1:], stdout, stderr)
	case "lock":
		err = runLock(ctx, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "filler-visual-corpus-nominate: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-visual-corpus-nominate:", err)
		return 1
	}
	return 0
}
