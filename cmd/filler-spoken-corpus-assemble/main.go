// Command filler-spoken-corpus-assemble creates one private, non-authorizing
// spoken-safety certification draft from independently prepared cohorts.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-spoken-corpus-assemble", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plan := flags.String("plan", "", "private spoken-safety assembly plan JSON")
	inputRoot := flags.String("input-root", "", "private root containing the policy and prepared cohorts")
	output := flags.String("output", "", "new private assembled review-draft directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *plan == "" || *inputRoot == "" || *output == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-corpus-assemble: exact plan, input root, and new output are required")
		return 2
	}
	result, err := fillersafetycorpus.AssembleReviewDraft(context.Background(), fillersafetycorpus.ReviewDraftConfig{
		PlanPath: *plan, InputRoot: *inputRoot, OutputDirectory: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-corpus-assemble:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-corpus-assemble: assembled %d cases (%d positive, %d clean); draft sha256 %s; worklist sha256 %s; %d input bytes; %d output bytes\n",
		result.Cases, result.PositiveFamilies, result.CleanFamilies, result.DraftSHA256, result.WorklistSHA256,
		result.InputBytes, result.OutputBytes)
	return 0
}
