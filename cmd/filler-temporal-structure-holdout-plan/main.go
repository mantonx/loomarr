// Command filler-temporal-structure-holdout-plan selects authority-bound source
// material and emits private construction authoring for the 36-case holdout.
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

type adjudicationPaths []string

func (paths *adjudicationPaths) String() string { return fmt.Sprint([]string(*paths)) }

func (paths *adjudicationPaths) Set(value string) error {
	if value == "" {
		return fmt.Errorf("prior adjudication path cannot be empty")
	}
	*paths = append(*paths, value)
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-temporal-structure-holdout-plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	selection := flags.String("selection", "", "frozen temporal selection JSON")
	evidence := flags.String("evidence", "", "public evidence manifest JSON")
	evidenceMap := flags.String("evidence-map", "", "private evidence map JSON")
	human := flags.String("human", "", "locked human assessment set JSON")
	humanAttestation := flags.String("human-attestation", "", "locked human attestation JSON")
	quality := flags.String("media-quality", "", "full-decode media-quality report JSON")
	suitability := flags.String("suitability", "", "two-family suitability comparison JSON")
	referenceAudit := flags.String("reference-audit", "", "reference audit bound by the duplicate-family authority")
	families := flags.String("families", "", "duplicate-family audit JSON")
	transitions := flags.String("transitions", "", "content-bound transition-edge authority JSON")
	programmes := flags.String("programmes", "", "programme-parent inventory JSON")
	sourceRoot := flags.String("source-root", "", "common root containing bounded and programme source media")
	seed := flags.String("seed", "", "private deterministic selection seed")
	seedFile := flags.String("seed-file", "", "file containing the private deterministic selection seed")
	genesis := flags.Bool("genesis", false, "declare a first holdout with no prior exposure")
	var priorAdjudications adjudicationPaths
	flags.Var(&priorAdjudications, "prior-adjudication", "immutable burned-challenge adjudication authority; repeat for cumulative replacement lineage")
	plannedText := flags.String("planned-at", "", "fixed RFC3339 planning time")
	output := flags.String("out", "", "new private plan directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	plannedAt, err := time.Parse(time.RFC3339, *plannedText)
	validLineage := *genesis && len(priorAdjudications) == 0 || !*genesis && len(priorAdjudications) > 0
	if err != nil || *selection == "" || *evidence == "" || *evidenceMap == "" || *human == "" || *humanAttestation == "" || *quality == "" || *suitability == "" || *referenceAudit == "" || *families == "" || *transitions == "" || *programmes == "" || *sourceRoot == "" || (*seed == "") == (*seedFile == "") || !validLineage || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-holdout-plan: selection, evidence, evidence map, human assessment, human attestation, media quality, suitability, reference audit, families, transitions, programmes, source root, private seed, exactly one --genesis or --prior-adjudication lineage mode, fixed planning time, and output are required")
		return 2
	}
	seedValue := *seed
	if *seedFile != "" {
		seedValue, err = fillerreview.LoadPrivateSeed(*seedFile)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-holdout-plan: read private seed file")
			return 2
		}
	}
	result, err := fillerreview.BuildTemporalStructureHoldoutPlan(fillerreview.TemporalStructureHoldoutConfig{
		SelectionPath: *selection, EvidenceManifestPath: *evidence, EvidencePrivateMapPath: *evidenceMap,
		HumanAssessmentPath: *human, HumanAttestationPath: *humanAttestation, MediaQualityPath: *quality,
		SuitabilityPath: *suitability, ReferenceAuditPath: *referenceAudit,
		FamilyAuditPath: *families, TransitionAuthorityPath: *transitions, ProgrammeInventoryPath: *programmes,
		SourceRoot: *sourceRoot, Seed: seedValue, Genesis: *genesis, PriorAdjudicationPaths: priorAdjudications,
		PlannedAt: plannedAt.UTC(), OutputDir: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-holdout-plan:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-holdout-plan: planned %d cases; authoring sha256 %s; receipt sha256 %s; training false; production admission false\n", result.Cases, result.AuthoringSHA256, result.ReceiptSHA256)
	return 0
}
