// Command filler-spoken-safety-project validates complete-source transcript
// authority and publishes an opaque source/derivative safety projection.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerreview"
)

type projectCapabilities struct {
	publish func(fillerreview.TemporalSpokenSafetyConfig) (fillerreview.TemporalSpokenSafetyReport, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, projectCapabilities{publish: fillerreview.PublishTemporalSpokenSafety}))
}

func run(args []string, stdout, stderr io.Writer, capabilities projectCapabilities) int {
	flags := flag.NewFlagSet("filler-spoken-safety-project", flag.ContinueOnError)
	flags.SetOutput(stderr)
	corpus := flags.String("corpus-manifest", "", "exact corpus manifest or draft")
	packets := flags.String("packets", "", "exact label-blind packet JSONL")
	corpusRoot := flags.String("corpus-root", "", "root containing packet media derivatives")
	corpusSplit := flags.String("corpus-split", "", "exact corpus split: development or holdout")
	evidenceVersion := flags.String("evidence-version", "", "exact packet evidence version")
	expectedCorpusCases := flags.Int("expected-corpus-cases", 0, "exact selected corpus case count")
	evidence := flags.String("evidence-manifest", "", "exact temporal evidence manifest")
	mapping := flags.String("evidence-private-map", "", "exact private evidence map")
	transcripts := flags.String("transcripts", "", "exact complete-source transcript JSONL")
	structure := flags.String("structure-manifest", "", "exact structure challenge manifest")
	authority := flags.String("structure-authority", "", "exact private structure authority")
	expectedCases := flags.Int("expected-structure-cases", 0, "exact structure case count")
	policy := flags.String("policy", "", "private spoken-safety policy")
	projectedAtRaw := flags.String("projected-at", "", "fixed RFC3339 projection time")
	output := flags.String("output", "", "new private projection JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *corpus == "" || *packets == "" || *corpusRoot == "" || *corpusSplit == "" || *evidenceVersion == "" || *expectedCorpusCases <= 0 || *evidence == "" || *mapping == "" || *transcripts == "" || *structure == "" || *authority == "" || *expectedCases <= 0 || *policy == "" || *projectedAtRaw == "" || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-project: every corpus, authority, transcript, policy, exact count, fixed time, and output flag is required")
		return 2
	}
	split := fillereval.Split(*corpusSplit)
	if split != fillereval.SplitDevelopment && split != fillereval.SplitHoldout {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-project: --corpus-split must be development or holdout")
		return 2
	}
	projectedAt, err := time.Parse(time.RFC3339Nano, *projectedAtRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-spoken-safety-project: --projected-at must be RFC3339")
		return 2
	}
	report, digest, err := capabilities.publish(fillerreview.TemporalSpokenSafetyConfig{
		CorpusManifestPath: *corpus, PacketsPath: *packets, CorpusRoot: *corpusRoot, CorpusSplit: split,
		EvidenceVersion: *evidenceVersion, ExpectedCorpusCases: *expectedCorpusCases,
		EvidenceManifestPath: *evidence, EvidencePrivateMapPath: *mapping, TranscriptSetPath: *transcripts,
		StructureManifestPath: *structure, StructureAuthorityPath: *authority, ExpectedStructureCases: *expectedCases,
		PolicyPath: *policy, ProjectedAt: projectedAt, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filler-spoken-safety-project: publish: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-spoken-safety-project: %d sources (%d prohibited, %d coverage holds, %d no-signal observations); %d derivative cases held; sha256 %s in %s\n", report.Sources, report.ProhibitedSources, report.CoverageHoldSources, report.NoSignalObservedSources, report.ProhibitedCases+report.CoverageHoldCases, digest, *output)
	return 0
}
