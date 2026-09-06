// Command filler-openrouter-snapshot freezes the exact model endpoints, prices,
// capabilities, and ZDR eligibility used to predeclare a paid filler bakeoff.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-openrouter-snapshot", flag.ContinueOnError)
	flags.SetOutput(stderr)
	modelsText := flags.String("models", "", "comma-separated concrete OpenRouter model IDs")
	outputModality := flags.String("output-modality", "", "optional OpenRouter output-modality catalog filter")
	outPath := flags.String("out", "", "immutable output snapshot JSON")
	baseURL := flags.String("base-url", fillerbakeoff.OpenRouterBaseURL, "OpenRouter API base URL")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *modelsText == "" || *outPath == "" {
		_, _ = fmt.Fprintln(stderr, "filler-openrouter-snapshot: --models and --out are required")
		return 2
	}
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		_, _ = fmt.Fprintln(stderr, "filler-openrouter-snapshot: OPENROUTER_API_KEY is required for the ZDR endpoint list")
		return 2
	}
	models := strings.Split(*modelsText, ",")
	for index := range models {
		models[index] = strings.TrimSpace(models[index])
	}
	snapshot, err := fillerbakeoff.FetchOpenRouterSnapshot(context.Background(), fillerbakeoff.OpenRouterSnapshotConfig{
		BaseURL: *baseURL, APIKey: apiKey, Models: models, OutputModality: *outputModality, RetrievedAt: time.Now().UTC(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filler-openrouter-snapshot: %v\n", err)
		return 1
	}
	if err := writeSnapshot(*outPath, snapshot); err != nil {
		_, _ = fmt.Fprintf(stderr, "filler-openrouter-snapshot: write snapshot: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-openrouter-snapshot: %d models, %d requests, %d response bytes; sha256 %s; %s\n",
		len(snapshot.Models), snapshot.Requests, snapshot.ResponseBytes, fillerbakeoff.OpenRouterSnapshotSHA256(snapshot), *outPath)
	return 0
}

func writeSnapshot(path string, snapshot fillerbakeoff.OpenRouterSnapshot) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".filler-openrouter-snapshot-*.json")
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
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, absolute); err != nil {
		return fmt.Errorf("publish immutable snapshot: %w", err)
	}
	if err := os.Remove(tempName); err != nil {
		_ = os.Remove(absolute)
		return err
	}
	ok = true
	return nil
}
