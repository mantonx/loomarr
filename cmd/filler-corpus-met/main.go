// Command filler-corpus-met freezes a bounded, metadata-only Met Museum
// inventory. It downloads no media and grants no rights or truth authority.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

const termSetSchemaVersion = 1

type termSet struct {
	SchemaVersion        int      `json:"schemaVersion"`
	Terms                []string `json:"terms"`
	RequiredSubjectTerms []string `json:"requiredSubjectTerms,omitempty"`
	ExcludedSubjectTerms []string `json:"excludedSubjectTerms,omitempty"`
}

type options struct {
	termsPath, outputPath, cacheDir, userAgent, roleHint string
	snapshotAt                                           time.Time
	maxRequests, maxObjectLookups, maxItems              int
	maxResponseBytes, maxItemBytes, maxTotalBytes        int64
	delay, maxWallTime                                   time.Duration
	http                                                 *http.Client
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-met", flag.ContinueOnError)
	flags.SetOutput(stderr)
	termsPath := flags.String("terms", "", "strict JSON file containing the frozen search-term set")
	outputPath := flags.String("out", "", "strict source-neutral inventory JSON")
	cacheDir := flags.String("cache-dir", "", "raw Met response and HEAD cache")
	userAgent := flags.String("user-agent", "", "descriptive User-Agent with contact")
	roleHint := flags.String("role-hint", "", "discovery-only role hint")
	snapshotText := flags.String("snapshot-at", "", "latest permitted source observation in RFC3339 format")
	maxRequests := flags.Int("max-requests", 0, "hard HTTP request ceiling")
	maxObjectLookups := flags.Int("max-object-lookups", 0, "hard object-record lookup ceiling")
	maxItems := flags.Int("max-items", 0, "exact candidate target and hard item ceiling")
	maxResponseBytes := flags.Int64("max-response-bytes", 0, "hard aggregate metadata-response ceiling")
	maxItemBytes := flags.Int64("max-item-bytes", 0, "hard predicted image-byte ceiling")
	maxTotalBytes := flags.Int64("max-total-bytes", 0, "hard predicted aggregate image-byte ceiling")
	delay := flags.Duration("delay", 250*time.Millisecond, "minimum delay between Met requests")
	maxWallTime := flags.Duration("max-wall-time", 30*time.Minute, "hard capture wall-time ceiling")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	snapshotAt, err := time.Parse(time.RFC3339, *snapshotText)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met: parse --snapshot-at:", err)
		return 2
	}
	opts := options{
		termsPath: *termsPath, outputPath: *outputPath, cacheDir: *cacheDir, userAgent: *userAgent,
		roleHint: *roleHint, snapshotAt: snapshotAt, maxRequests: *maxRequests,
		maxObjectLookups: *maxObjectLookups, maxItems: *maxItems, maxResponseBytes: *maxResponseBytes,
		maxItemBytes: *maxItemBytes, maxTotalBytes: *maxTotalBytes, delay: *delay, maxWallTime: *maxWallTime,
	}
	if opts.termsPath == "" || opts.outputPath == "" || opts.cacheDir == "" || opts.userAgent == "" || opts.roleHint == "" {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met: terms, output, cache, identity, role, UTC snapshot, and explicit request/item/byte/time ceilings are required")
		return 2
	}
	termSet, err := readTermSet(opts.termsPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met: read terms:", err)
		return 1
	}
	for _, directory := range []string{opts.cacheDir, filepath.Dir(opts.outputPath)} {
		if err := ensurePrivateDirectory(directory); err != nil {
			_, _ = fmt.Fprintln(stderr, "filler-corpus-met: prepare private path:", err)
			return 1
		}
	}
	inventory, err := capture(context.Background(), termSet, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met:", err)
		return 1
	}
	if err := writeJSON(opts.outputPath, inventory); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-met: write inventory:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-met: froze %d candidates (%d predicted bytes) in %d requests\n", len(inventory.Cases), inventory.Captures[0].PredictedMediaBytes, inventory.Captures[0].RequestsUsed)
	return 0
}

func capture(ctx context.Context, terms termSet, opts options) (fillercorpus.Inventory, error) {
	httpClient := opts.http
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return fillercorpus.CaptureMetInventory(ctx, fillercorpus.MetCaptureConfig{
		HTTP: httpClient, CacheDir: opts.cacheDir, UserAgent: opts.userAgent, Terms: terms.Terms,
		RequiredSubjectTerms: terms.RequiredSubjectTerms, ExcludedSubjectTerms: terms.ExcludedSubjectTerms,
		RoleHint: opts.roleHint, SnapshotAt: opts.snapshotAt, MaxRequests: opts.maxRequests,
		MaxObjectLookups: opts.maxObjectLookups, MaxItems: opts.maxItems,
		MaxResponseBytes: opts.maxResponseBytes, MaxItemBytes: opts.maxItemBytes,
		MaxTotalBytes: opts.maxTotalBytes, Delay: opts.delay, MaxWallTime: opts.maxWallTime,
	})
}

func readTermSet(filename string) (termSet, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return termSet{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value termSet
	if err := decoder.Decode(&value); err != nil {
		return termSet{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF || value.SchemaVersion != termSetSchemaVersion || len(value.Terms) == 0 {
		return termSet{}, fmt.Errorf("exactly one schema-1 term set is required")
	}
	return value, nil
}

func writeJSON(filename string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	parent := filepath.Dir(filename)
	if err := ensurePrivateDirectory(parent); err != nil {
		return err
	}
	temp, err := os.CreateTemp(parent, ".filler-corpus-met-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filename); err != nil {
		return err
	}
	ok = true
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a regular directory", path)
	}
	if info.Mode().Perm() != 0o700 {
		return os.Chmod(path, 0o700)
	}
	return nil
}
