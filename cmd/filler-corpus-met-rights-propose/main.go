// Command filler-corpus-met-rights-propose reduces frozen Met object evidence
// into a complete, non-authorizing anomaly report. It performs no network or
// media request and cannot produce downloader approvals.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

const (
	maximumInventoryBytes      int64 = 64 << 20
	maximumPolicyEvidenceBytes int64 = 1 << 20
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-met-rights-propose", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "frozen schema-4 Met inventory JSON")
	metadataRoot := flags.String("metadata-cache", "", "private frozen Met metadata cache directory")
	policyEvidencePath := flags.String("policy-evidence", "", "pinned non-authorizing Met policy-evidence JSON")
	outputPath := flags.String("out", "", "private path-free pre-screen report JSON")
	preparedAtText := flags.String("prepared-at", "", "pre-screen time in RFC3339 format")
	minItems := flags.Int("min-items", 0, "required minimum inventory coverage")
	maxItems := flags.Int("max-items", 0, "hard inventory coverage ceiling")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *inventoryPath == "" || *metadataRoot == "" || *policyEvidencePath == "" || *outputPath == "" || *preparedAtText == "" || *minItems <= 0 || *maxItems < *minItems {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-propose: inventory, metadata cache, policy evidence, output, preparation time, and positive min/max item bounds are required")
		return 2
	}
	preparedAt, err := time.Parse(time.RFC3339, *preparedAtText)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-propose: parse --prepared-at:", err)
		return 2
	}
	inventoryRaw, err := readBoundedRegularFile(*inventoryPath, maximumInventoryBytes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-propose: read inventory:", err)
		return 1
	}
	policyEvidenceRaw, err := readBoundedRegularFile(*policyEvidencePath, maximumPolicyEvidenceBytes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-propose: read policy evidence:", err)
		return 1
	}
	report, err := fillercorpus.PrepareMetRightsPrescreen(inventoryRaw, policyEvidenceRaw, fillercorpus.MetRightsPrescreenOptions{
		MetadataRoot: *metadataRoot, PreparedAt: preparedAt, MinItems: *minItems, MaxItems: *maxItems,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-propose:", err)
		return 1
	}
	if err := writePrivateJSONExclusive(*outputPath, report); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met-rights-propose: publish report:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-met-rights-propose: pre-screened %d Met candidates (%d pass, %d hold); rightsApproval=false downloadAuthority=false\n", report.TotalCases, report.PassedCases, report.HeldCases)
	return 0
}
