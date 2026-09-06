// Command filler-corpus-met-rights-complete prepares one digest-bound Met
// rights attestation or expands an accepted attestation into the existing
// item-level CSV review contract. It never emits downloader authority.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

const (
	maximumInventoryBytes   int64 = 64 << 20
	maximumWorksheetBytes   int64 = 64 << 20
	maximumPrescreenBytes   int64 = 16 << 20
	maximumAttestationBytes int64 = 1 << 20
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-met-rights-complete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "", "required transition: prepare or complete")
	inventoryPath := flags.String("inventory", "", "private frozen schema-4 Met inventory JSON")
	worksheetPath := flags.String("worksheet", "", "private inert development rights worksheet JSON")
	prescreenPath := flags.String("prescreen", "", "private complete zero-anomaly Met pre-screen JSON")
	attestationPath := flags.String("attestation", "", "private accepted batch-attestation JSON")
	attestationOutputPath := flags.String("attestation-out", "", "private pending batch-attestation JSON")
	completedCSVOutputPath := flags.String("completed-csv-out", "", "private completed item-level review CSV")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *inventoryPath == "" || *worksheetPath == "" || *prescreenPath == "" || (*mode != "prepare" && *mode != "complete") {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: mode, inventory, worksheet, and pre-screen are required")
		return 2
	}
	if (*mode == "prepare" && (*attestationOutputPath == "" || *attestationPath != "" || *completedCSVOutputPath != "")) ||
		(*mode == "complete" && (*attestationPath == "" || *completedCSVOutputPath == "" || *attestationOutputPath != "")) {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: prepare requires only --attestation-out; complete requires only --attestation and --completed-csv-out")
		return 2
	}
	inventoryRaw, err := readPrivateBoundedRegularFile(*inventoryPath, maximumInventoryBytes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: read inventory:", err)
		return 1
	}
	worksheetRaw, err := readPrivateBoundedRegularFile(*worksheetPath, maximumWorksheetBytes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: read worksheet:", err)
		return 1
	}
	prescreenRaw, err := readPrivateBoundedRegularFile(*prescreenPath, maximumPrescreenBytes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: read pre-screen:", err)
		return 1
	}
	if *mode == "prepare" {
		template, err := fillercorpus.PrepareMetRightsBatchAttestation(inventoryRaw, worksheetRaw, prescreenRaw)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete:", err)
			return 1
		}
		if err := writePrivateExclusive(*attestationOutputPath, func(writer io.Writer) error {
			encoder := json.NewEncoder(writer)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			return encoder.Encode(template)
		}); err != nil {
			_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: publish attestation template:", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "filler-corpus-met-rights-complete: prepared one pending attestation for %s; downloadAuthority=false\n", template.InventorySHA256)
		return 0
	}
	attestationRaw, err := readPrivateBoundedRegularFile(*attestationPath, maximumAttestationBytes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: read attestation:", err)
		return 1
	}
	completion, err := fillercorpus.CompleteMetRightsBatchReview(inventoryRaw, worksheetRaw, prescreenRaw, attestationRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete:", err)
		return 1
	}
	if err := writePrivateExclusive(*completedCSVOutputPath, func(writer io.Writer) error {
		_, err := writer.Write(completion.CompletedCSV)
		return err
	}); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-complete: publish completed review:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-met-rights-complete: expanded attestation %s into %d item-bound review rows; downloadAuthority=false\n", completion.AttestationSHA256, completion.RowCount)
	return 0
}
