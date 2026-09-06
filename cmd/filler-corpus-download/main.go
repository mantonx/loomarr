// Command filler-corpus-download downloads only independently rights-approved
// corpus media under explicit request, item, byte, and image-pixel ceilings.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

const maximumDownloadedImagePixels = fillercorpus.MaximumMaterializedImagePixels

type downloadLedger = fillercorpus.MaterializationLedger
type downloadedCase = fillercorpus.MaterializedCase

type plannedDownload struct {
	candidate fillercorpus.InventoryCase
	approval  fillercorpus.RightsDecision
	path      string
}

type options struct {
	inventoryPath, approvalsPath, outputDir, ledgerPath, userAgent string
	profile, processorID, processorTermsSHA256                     string
	inventorySHA256                                                string
	generatedAt                                                    time.Time
	maxRequests, maxItems                                          int
	maxBytes, maxImagePixels                                       int64
	delay                                                          time.Duration
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-download", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "frozen source inventory JSON")
	approvalsPath := flags.String("rights-approvals", "", "independent rights decisions JSONL")
	outputDir := flags.String("out-dir", "", "private corpus media directory")
	ledgerPath := flags.String("ledger", "", "content-addressed download ledger JSON")
	userAgent := flags.String("user-agent", "", "descriptive source User-Agent with contact")
	generatedAtText := flags.String("generated-at", "", "ledger generation time in RFC3339 format")
	maxRequests := flags.Int("max-requests", 0, "hard HTTP request ceiling")
	maxItems := flags.Int("max-items", 0, "hard approved item ceiling")
	maxBytes := flags.Int64("max-bytes", 0, "hard approved media-byte ceiling")
	maxImagePixels := flags.Int64("max-image-pixels", maximumDownloadedImagePixels, "hard decoded-image pixel ceiling")
	delay := flags.Duration("delay", time.Second, "minimum delay between HTTP requests")
	profile := flags.String("profile", "", "required rights profile: development or certification")
	processorID := flags.String("processor-id", "", "exact approved inference processor identifier (certification only)")
	processorTermsSHA256 := flags.String("processor-terms-sha256", "", "SHA-256 of approved processor terms snapshot (certification only)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	profileValid := *profile == fillercorpus.RightsProfileDevelopment || *profile == fillercorpus.RightsProfileCertification
	certificationIdentityValid := *profile != fillercorpus.RightsProfileCertification || (strings.TrimSpace(*processorID) != "" && fillercorpus.IsSHA256(*processorTermsSHA256))
	if *inventoryPath == "" || *approvalsPath == "" || *outputDir == "" || *ledgerPath == "" || *userAgent == "" || *generatedAtText == "" || *maxRequests <= 0 || *maxItems <= 0 || *maxBytes <= 0 || *maxImagePixels <= 0 || *maxImagePixels > maximumDownloadedImagePixels || *delay < 500*time.Millisecond || !profileValid || !certificationIdentityValid {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: inventory, rights approvals, private output, ledger, identified User-Agent, generation time, explicit development/certification profile, positive ceilings, <=50m image pixels, >=500ms delay, and certification processor identity are required")
		return 2
	}
	generatedAt, err := time.Parse(time.RFC3339, *generatedAtText)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: parse --generated-at:", err)
		return 2
	}
	opts := options{
		inventoryPath: *inventoryPath, approvalsPath: *approvalsPath, outputDir: *outputDir,
		ledgerPath: *ledgerPath, userAgent: *userAgent, generatedAt: generatedAt,
		profile: *profile, processorID: *processorID, processorTermsSHA256: *processorTermsSHA256,
		maxRequests: *maxRequests, maxItems: *maxItems, maxBytes: *maxBytes,
		maxImagePixels: *maxImagePixels, delay: *delay,
	}
	inv, inventorySHA256, err := readInventory(opts.inventoryPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: read inventory:", err)
		return 1
	}
	opts.inventorySHA256 = inventorySHA256
	approvals, err := readJSONL[fillercorpus.RightsDecision](opts.approvalsPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: read approvals:", err)
		return 1
	}
	plan, err := planDownloads(inv, approvals, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download:", err)
		return 1
	}
	ledger, err := executeDownloads(context.Background(), &http.Client{Timeout: 5 * time.Minute}, plan, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download:", err)
		return 1
	}
	ledger.InventorySHA256 = inventorySHA256
	if err := fillercorpus.ValidateMaterializationLedger(ledger, inv, inventorySHA256); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: validate ledger:", err)
		return 1
	}
	if err := writeJSON(opts.ledgerPath, ledger); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: write ledger:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-download: locked %d files (%d bytes) in %d requests\n", len(ledger.Cases), ledger.Bytes, ledger.RequestsUsed)
	return 0
}
