// Command filler-temporal-structure-holdout-plan selects authority-bound source
// material and emits private construction authoring for the 60-case holdout.
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
	referenceDownloadLedger := flags.String("reference-download-ledger", "", "exact download ledger digest-bound by the reference audit")
	families := flags.String("families", "", "duplicate-family audit JSON")
	transitions := flags.String("transitions", "", "content-bound transition-edge authority JSON")
	programmes := flags.String("programmes", "", "programme-parent inventory JSON")
	sourceRoot := flags.String("source-root", "", "common root containing bounded and programme source media")
	seed := flags.String("seed", "", "private deterministic selection seed")
	seedFile := flags.String("seed-file", "", "file containing the private deterministic selection seed")
	plannedText := flags.String("planned-at", "", "fixed RFC3339 planning time")
	output := flags.String("out", "", "new private plan directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	plannedAt, err := time.Parse(time.RFC3339, *plannedText)
	if err != nil || *selection == "" || *evidence == "" || *evidenceMap == "" || *human == "" || *humanAttestation == "" || *quality == "" || *suitability == "" || *referenceAudit == "" || *referenceDownloadLedger == "" || *families == "" || *transitions == "" || *programmes == "" || *sourceRoot == "" || (*seed == "") == (*seedFile == "") || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-holdout-plan: selection, evidence, evidence map, human assessment, human attestation, media quality, suitability, reference audit, reference download ledger, families, transitions, programmes, source root, private seed, fixed planning time, and output are required")
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
		SuitabilityPath: *suitability, ReferenceAuditPath: *referenceAudit, ReferenceDownloadLedgerPath: *referenceDownloadLedger,
		FamilyAuditPath: *families, TransitionAuthorityPath: *transitions, ProgrammeInventoryPath: *programmes,
		SourceRoot: *sourceRoot, Seed: seedValue, PlannedAt: plannedAt.UTC(), OutputDir: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-holdout-plan:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-holdout-plan: planned %d cases; authoring sha256 %s; receipt sha256 %s; training false; production admission false\n", result.Cases, result.AuthoringSHA256, result.ReceiptSHA256)
	return 0
}
