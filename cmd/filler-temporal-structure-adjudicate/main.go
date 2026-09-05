// Command filler-temporal-structure-adjudicate publishes immutable authority
// for targeted complete-playback review of challenged standalone anchors.
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

type assessmentPaths []string

func (paths *assessmentPaths) String() string { return fmt.Sprint([]string(*paths)) }

func (paths *assessmentPaths) Set(value string) error {
	if value == "" {
		return fmt.Errorf("assessment path cannot be empty")
	}
	*paths = append(*paths, value)
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-temporal-structure-adjudicate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	public := flags.String("public", "", "public temporal-structure manifest JSON")
	authority := flags.String("authority", "", "coordinator-private temporal-structure authority JSON")
	planAuthoring := flags.String("plan-authoring", "", "private holdout plan authoring JSON")
	planReceipt := flags.String("plan-receipt", "", "private holdout plan receipt JSON")
	var assessments assessmentPaths
	flags.Var(&assessments, "assessment", "locked full-video assessment JSON; repeat in the opened comparison panel")
	comparison := flags.String("comparison", "", "opened temporal-structure comparison JSON")
	submission := flags.String("submission", "", "targeted complete-playback human submission JSON")
	cases := flags.Int("cases", 0, "exact expected challenge case count")
	adjudicatedText := flags.String("adjudicated-at", "", "fixed RFC3339 authority publication time")
	output := flags.String("out", "", "new immutable private adjudication authority JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	adjudicatedAt, err := time.Parse(time.RFC3339, *adjudicatedText)
	if err != nil || *public == "" || *authority == "" || *planAuthoring == "" || *planReceipt == "" || len(assessments) < 2 || *comparison == "" || *submission == "" || *cases <= 0 || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-adjudicate: challenge authority, bound plan authoring and receipt, every opened --assessment, comparison, complete-playback submission, positive exact case count, fixed adjudication time, and output are required")
		return 2
	}
	result, err := fillerreview.PublishTemporalStructureAnchorAdjudication(fillerreview.TemporalStructureAnchorAdjudicationConfig{
		PublicManifestPath: *public, PrivateAuthorityPath: *authority,
		PlanAuthoringPath: *planAuthoring, PlanReceiptPath: *planReceipt,
		AssessmentPaths: assessments, ComparisonPath: *comparison, SubmissionPath: *submission,
		ExpectedCases: *cases, AdjudicatedAt: adjudicatedAt.UTC(), OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-adjudicate:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-adjudicate: adjudicated %d challenged anchors; challenge burned; score repair false; training false; production admission false; sha256 %s; %s\n", result.Cases, result.AuthoritySHA256, *output)
	return 0
}
