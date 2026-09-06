// Command filler-spoken-cascade-authority locks reviewed source truth into a
// private, path-free authority before any certification evaluation runs.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-spoken-cascade-authority", flag.ContinueOnError)
	flags.SetOutput(stderr)
	draft := flags.String("draft", "", "private source and truth draft JSON")
	firstReview := flags.String("first-review", "", "first independent primary review JSON")
	secondReview := flags.String("second-review", "", "second independent primary review JSON")
	adjudicator := flags.String("adjudicator", "", "independent adjudication JSON for disputed cases")
	seed := flags.String("seed", "", "private alias seed file")
	sourceRoot := flags.String("source-root", "", "private root containing source and evidence files")
	authored := flags.String("authored-at", "", "fixed RFC3339 authority time")
	expectedCases := flags.Int("expected-cases", 0, "exact predeclared case count")
	maximumSourceBytes := flags.Int64("max-source-bytes", 0, "maximum aggregate source bytes")
	output := flags.String("output", "", "new private authority JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	authoredAt, err := time.Parse(time.RFC3339, *authored)
	if err != nil || *draft == "" || *firstReview == "" || *secondReview == "" || *seed == "" ||
		*sourceRoot == "" || *expectedCases <= 0 || *maximumSourceBytes <= 0 || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-cascade-authority: --draft, two reviews, --seed, --source-root, valid --authored-at, positive ceilings, and --output are required")
		return 2
	}
	evidenceValidator := fillersafetycorpus.NewCertificationEvidenceValidator()
	result, err := fillersafetycert.BuildAuthority(context.Background(), fillersafetycert.AuthorityBuildConfig{
		DraftPath: *draft, FirstReviewPath: *firstReview, SecondReviewPath: *secondReview,
		AdjudicatorPath: *adjudicator, SeedPath: *seed, SourceRoot: *sourceRoot,
		AuthoredAt: authoredAt.UTC(), ExpectedCases: *expectedCases,
		MaximumSourceBytes: *maximumSourceBytes,
		ValidateEvidence:   evidenceValidator.Validate,
		OutputPath:         *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-cascade-authority:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-cascade-authority: locked %d cases (%d positive, %d clean); authority sha256 %s\n",
		result.Cases, result.PositiveFamilies, result.CleanFamilies, result.AuthoritySHA256)
	return 0
}
