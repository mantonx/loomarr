// Command filler-corpus-download downloads only independently rights-approved
// corpus media under explicit request, item, and byte ceilings.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

type downloadLedger = fillercorpus.DownloadLedger

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
	maxBytes                                                       int64
	delay                                                          time.Duration
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-download", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "frozen source inventory JSON")
	approvalsPath := flags.String("rights-approvals", "", "independent rights decisions JSONL")
	outputDir := flags.String("out-dir", "", "external corpus media directory")
	ledgerPath := flags.String("ledger", "", "content-addressed download ledger JSON")
	userAgent := flags.String("user-agent", "", "descriptive source User-Agent with contact")
	generatedAtText := flags.String("generated-at", "", "ledger generation time in RFC3339 format")
	maxRequests := flags.Int("max-requests", 0, "hard HTTP request ceiling")
	maxItems := flags.Int("max-items", 0, "hard approved item ceiling")
	maxBytes := flags.Int64("max-bytes", 0, "hard approved media-byte ceiling")
	delay := flags.Duration("delay", time.Second, "minimum delay between HTTP requests")
	profile := flags.String("profile", "", "required rights profile: quarantine, development, or certification")
	processorID := flags.String("processor-id", "", "exact approved inference processor identifier (certification only)")
	processorTermsSHA256 := flags.String("processor-terms-sha256", "", "SHA-256 of approved processor terms snapshot (certification only)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	profileValid := fillercorpus.KnownRightsProfile(*profile)
	certificationIdentityValid := *profile != fillercorpus.RightsProfileCertification || (strings.TrimSpace(*processorID) != "" && fillercorpus.IsSHA256(*processorTermsSHA256))
	if *inventoryPath == "" || *approvalsPath == "" || *outputDir == "" || *ledgerPath == "" || *userAgent == "" || *generatedAtText == "" || *maxRequests <= 0 || *maxItems <= 0 || *maxBytes <= 0 || *delay < 500*time.Millisecond || !profileValid || !certificationIdentityValid {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: inventory, rights approvals, output, ledger, identified User-Agent, generation time, explicit quarantine/development/certification profile, positive ceilings, >=500ms delay, and certification processor identity are required")
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
		maxRequests: *maxRequests, maxItems: *maxItems, maxBytes: *maxBytes, delay: *delay,
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
	if err := requireNewLedger(opts.ledgerPath); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download:", err)
		return 1
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	ledger, err := executeDownloads(context.Background(), client, plan, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download:", err)
		return 1
	}
	ledger.InventorySHA256 = inventorySHA256
	if err := writeJSON(opts.ledgerPath, ledger); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-download: write ledger:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-download: locked %d files (%d bytes) in %d requests\n", len(ledger.Cases), ledger.Bytes, ledger.RequestsUsed)
	return 0
}

func planDownloads(inv fillercorpus.Inventory, approvals []fillercorpus.RightsDecision, opts options) ([]plannedDownload, error) {
	if failures := fillercorpus.ValidateInventory(inv); len(failures) != 0 {
		return nil, fmt.Errorf("invalid inventory: %s", strings.Join(failures, "; "))
	}
	byID := make(map[string]fillercorpus.InventoryCase, len(inv.Cases))
	for _, candidate := range inv.Cases {
		if _, duplicate := byID[candidate.CaseID]; duplicate {
			return nil, fmt.Errorf("duplicate inventory candidate %s", candidate.CaseID)
		}
		byID[candidate.CaseID] = candidate
	}
	seen := map[string]struct{}{}
	var plan []plannedDownload
	var bytes int64
	for _, approval := range approvals {
		if _, duplicate := seen[approval.CaseID]; duplicate {
			return nil, fmt.Errorf("duplicate rights decision for %s", approval.CaseID)
		}
		seen[approval.CaseID] = struct{}{}
		candidate, ok := byID[approval.CaseID]
		if !ok {
			return nil, fmt.Errorf("rights-reviewed item %s is absent from the inventory", approval.CaseID)
		}
		if approval.InventorySHA256 != opts.inventorySHA256 {
			return nil, fmt.Errorf("rights-reviewed item %s is not tied to the frozen inventory", approval.CaseID)
		}
		if !slices.Equal(approval.CaptureIDs, candidate.CaptureIDs) || approval.Authority != candidate.Authority || approval.ItemID != candidate.ItemID {
			return nil, fmt.Errorf("rights-reviewed item %s changes its source identity", approval.CaseID)
		}
		if approval.Decision != "approved" && approval.Decision != "held" {
			return nil, fmt.Errorf("rights-reviewed item %s has unknown decision %q", approval.CaseID, approval.Decision)
		}
		if approval.MetadataSHA256 != candidate.MetadataSHA256 || approval.ReviewerID == "" || approval.ReviewedAt.IsZero() || approval.ReviewedAt.Before(candidate.MetadataRetrievedAt) || approval.ReviewedAt.After(opts.generatedAt) || strings.TrimSpace(approval.Basis) == "" {
			return nil, fmt.Errorf("rights-reviewed item %s is not tied to its metadata and complete review", approval.CaseID)
		}
		if err := validateDecisionProfile(approval, opts); err != nil {
			return nil, fmt.Errorf("rights-reviewed item %s: %w", approval.CaseID, err)
		}
		if approval.Decision == "held" {
			continue
		}
		if requiresCredit(candidate.LicenseURL) && strings.TrimSpace(approval.RequiredCredit) == "" {
			return nil, fmt.Errorf("approved item %s requires attribution", approval.CaseID)
		}
		if candidate.Representation.Transport == fillercorpus.TransportLocal {
			continue
		}
		if err := fillercorpus.ValidateMediaURL(candidate.Representation.URL, candidate.AllowedMediaHosts); err != nil {
			return nil, err
		}
		bytes += candidate.Representation.Bytes
		ext := filepath.Ext(candidate.Representation.Name)
		name := fillercorpus.InventorySHA256([]byte(candidate.CaseID))[:16] + ext
		plan = append(plan, plannedDownload{candidate: candidate, approval: approval, path: filepath.Join(opts.outputDir, name)})
	}
	if len(plan) == 0 {
		return nil, fmt.Errorf("rights ledger approves no media")
	}
	if len(plan) > opts.maxItems || bytes > opts.maxBytes {
		return nil, fmt.Errorf("approved plan has %d items and %d bytes; ceilings are %d and %d", len(plan), bytes, opts.maxItems, opts.maxBytes)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].candidate.CaseID < plan[j].candidate.CaseID })
	return plan, nil
}

func validateDecisionProfile(approval fillercorpus.RightsDecision, opts options) error {
	switch opts.profile {
	case fillercorpus.RightsProfileQuarantine:
		if approval.HoldoutContract != nil || approval.QuarantineContract == nil || approval.Redistributable {
			return fmt.Errorf("decision is not restricted to quarantine")
		}
		reasons := fillercorpus.QuarantineAcquisitionHoldReasons(approval.QuarantineContract)
		if approval.Decision == "approved" && (len(reasons) != 0 || len(approval.QuarantineContract.HoldReasons) != 0) {
			return fmt.Errorf("approval lacks exact quarantine authority")
		}
		if approval.Decision == "held" && len(approval.QuarantineContract.HoldReasons) == 0 {
			return fmt.Errorf("held quarantine decision has no hold reasons")
		}
	case fillercorpus.RightsProfileCertification:
		if approval.QuarantineContract != nil || approval.HoldoutContract == nil {
			return fmt.Errorf("decision lacks a certification holdout contract")
		}
		contract := approval.HoldoutContract
		reasons := fillercorpus.HoldoutRightsHoldReasons(contract, opts.generatedAt)
		if approval.Decision == "approved" && (len(reasons) != 0 || contract.ProcessorID != opts.processorID || contract.ProcessorTermsSHA256 != opts.processorTermsSHA256 || len(contract.HoldReasons) != 0) {
			return fmt.Errorf("approval lacks the exact certification holdout authority")
		}
	case fillercorpus.RightsProfileDevelopment:
		if approval.QuarantineContract != nil || approval.HoldoutContract != nil || approval.Redistributable != (approval.Decision == "approved") {
			return fmt.Errorf("decision is not exact development authority")
		}
	default:
		return fmt.Errorf("unknown rights profile %q", opts.profile)
	}
	return nil
}

func executeDownloads(ctx context.Context, client *http.Client, plan []plannedDownload, opts options) (downloadLedger, error) {
	if err := os.MkdirAll(opts.outputDir, 0o750); err != nil {
		return downloadLedger{}, err
	}
	ledger := downloadLedger{SchemaVersion: fillercorpus.DownloadLedgerSchemaVersion, Profile: opts.profile, GeneratedAt: opts.generatedAt.UTC(), MaxRequests: opts.maxRequests, MaxItems: opts.maxItems, MaxBytes: opts.maxBytes}
	budget := requestBudget{max: opts.maxRequests}
	lastRequestAt := time.Time{}
	for _, item := range plan {
		verifiedAt := opts.generatedAt.UTC()
		hashes, size, err := hashFile(item.path)
		if errors.Is(err, os.ErrNotExist) {
			if budget.used >= budget.max {
				return downloadLedger{}, fmt.Errorf("request ceiling exhausted before %s", item.candidate.CaseID)
			}
			if !lastRequestAt.IsZero() {
				wait := opts.delay - time.Since(lastRequestAt)
				if wait > 0 {
					timer := time.NewTimer(wait)
					select {
					case <-ctx.Done():
						timer.Stop()
						return downloadLedger{}, ctx.Err()
					case <-timer.C:
					}
				}
			}
			hashes, size, err = download(ctx, client, item, opts.userAgent, &budget)
			lastRequestAt = time.Now()
			verifiedAt = lastRequestAt.UTC()
		}
		if err != nil {
			return downloadLedger{}, fmt.Errorf("%s: %w", item.candidate.CaseID, err)
		}
		if size != item.candidate.Representation.Bytes || !matchesOptional(hashes.sha256, item.candidate.Representation.SHA256) || !matchesOptional(hashes.sha1, item.candidate.Representation.SHA1) || !matchesOptional(hashes.md5, item.candidate.Representation.MD5) {
			return downloadLedger{}, fmt.Errorf("%s: downloaded bytes or source checksums do not match inventory", item.candidate.CaseID)
		}
		ledger.Bytes += size
		ledger.Cases = append(ledger.Cases, fillercorpus.DownloadCase{
			CaseID: item.candidate.CaseID, Authority: item.candidate.Authority, ItemID: item.candidate.ItemID, LicenseURL: item.candidate.LicenseURL,
			ItemURL: item.candidate.ItemURL, MetadataURL: item.candidate.MetadataURL,
			MetadataRetrievedAt: item.candidate.MetadataRetrievedAt, MetadataSHA256: item.candidate.MetadataSHA256,
			Representation: item.candidate.Representation, LocalFile: filepath.Base(item.path), ContentSHA256: hashes.sha256,
			Approval: item.approval, VerifiedAt: verifiedAt,
		})
	}
	ledger.RequestsUsed = budget.used
	return ledger, nil
}

type fileHashes struct{ sha256, sha1, md5 string }

func download(ctx context.Context, client *http.Client, item plannedDownload, userAgent string, budget *requestBudget) (fileHashes, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.candidate.Representation.URL, nil)
	if err != nil {
		return fileHashes{}, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	if err := budget.consume(); err != nil {
		return fileHashes{}, 0, err
	}
	requestClient := *client
	requestClient.CheckRedirect = redirectPolicy(item.candidate.AllowedMediaHosts, budget)
	resp, err := requestClient.Do(req)
	if err != nil {
		return fileHashes{}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fileHashes{}, 0, fmt.Errorf("GET: %s", resp.Status)
	}
	if resp.ContentLength > item.candidate.Representation.Bytes && resp.ContentLength >= 0 {
		return fileHashes{}, 0, fmt.Errorf("Content-Length exceeds inventory size")
	}
	temp, err := os.CreateTemp(filepath.Dir(item.path), ".filler-corpus-download-*")
	if err != nil {
		return fileHashes{}, 0, err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fileHashes{}, 0, err
	}
	hashes, n, err := copyAndHash(temp, io.LimitReader(resp.Body, item.candidate.Representation.Bytes+1))
	if err != nil {
		return fileHashes{}, 0, err
	}
	if n != item.candidate.Representation.Bytes {
		return fileHashes{}, 0, fmt.Errorf("received %d bytes, want %d", n, item.candidate.Representation.Bytes)
	}
	if !matchesOptional(hashes.sha256, item.candidate.Representation.SHA256) || !matchesOptional(hashes.sha1, item.candidate.Representation.SHA1) || !matchesOptional(hashes.md5, item.candidate.Representation.MD5) {
		return fileHashes{}, 0, fmt.Errorf("source checksums do not match inventory")
	}
	if err := temp.Close(); err != nil {
		return fileHashes{}, 0, err
	}
	if err := os.Rename(tempName, item.path); err != nil {
		return fileHashes{}, 0, err
	}
	ok = true
	return hashes, n, nil
}

func redirectPolicy(allowedHosts []string, budget *requestBudget) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if req.URL == nil {
			return fmt.Errorf("refuse redirect without a destination URL")
		}
		if err := fillercorpus.ValidateMediaURL(req.URL.String(), allowedHosts); err != nil {
			return fmt.Errorf("refuse redirect: %w", err)
		}
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return budget.consume()
	}
}

type requestBudget struct {
	used int
	max  int
}

func (budget *requestBudget) consume() error {
	if budget == nil || budget.max <= 0 || budget.used >= budget.max {
		return fmt.Errorf("request ceiling exhausted")
	}
	budget.used++
	return nil
}

func hashFile(path string) (fileHashes, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileHashes{}, 0, err
	}
	defer func() { _ = file.Close() }()
	return copyAndHash(io.Discard, file)
}

func copyAndHash(destination io.Writer, source io.Reader) (fileHashes, int64, error) {
	sha256Hash, sha1Hash, md5Hash := sha256.New(), sha1.New(), md5.New()
	writers := []io.Writer{destination, sha256Hash, sha1Hash, md5Hash}
	n, err := io.Copy(io.MultiWriter(writers...), source)
	if err != nil {
		return fileHashes{}, n, err
	}
	return fileHashes{sha256: sumHex(sha256Hash), sha1: sumHex(sha1Hash), md5: sumHex(md5Hash)}, n, nil
}

func sumHex(value hash.Hash) string { return hex.EncodeToString(value.Sum(nil)) }

func matchesOptional(actual, expected string) bool {
	return expected == "" || strings.EqualFold(actual, expected)
}

func requiresCredit(license string) bool {
	normalized := strings.ToLower(license)
	return strings.Contains(normalized, "/licenses/by/") || strings.Contains(normalized, "/licenses/by-sa/")
}

func readInventory(path string) (fillercorpus.Inventory, string, error) {
	var value fillercorpus.Inventory
	data, err := os.ReadFile(path)
	if err != nil {
		return value, "", err
	}
	value, err = fillercorpus.DecodeInventoryBytes(data)
	if err != nil {
		return value, "", err
	}
	sum := sha256.Sum256(data)
	return value, hex.EncodeToString(sum[:]), nil
}

func readJSONL[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var values []T
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var value T
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			if err == nil {
				err = fmt.Errorf("trailing JSON value")
			}
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		values = append(values, value)
	}
	return values, scanner.Err()
}

func requireNewLedger(path string) error {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return fmt.Errorf("ledger output already exists")
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("inspect ledger output: %w", err)
	}
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".filler-corpus-ledger-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, absolute); err != nil {
		return fmt.Errorf("publish immutable download ledger: %w", err)
	}
	if err := os.Remove(tempName); err != nil {
		_ = os.Remove(absolute)
		return err
	}
	ok = true
	return nil
}
