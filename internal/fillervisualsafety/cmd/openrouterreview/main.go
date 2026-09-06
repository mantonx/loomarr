// Command openrouterreview performs one serial, candidate-blind visual-policy
// review over a previously verified bundle. It never creates truth or admission.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

const maximumRequestBytes = int64(2 << 20)

type request struct {
	BundlePath              string `json:"bundlePath"`
	ExpectedPackageSHA256   string `json:"expectedPackageSha256"`
	ExpectedOwnerMapSHA256  string `json:"expectedOwnerMapSha256"`
	ExpectedSelectionOrigin string `json:"expectedSelectionOrigin"`
	FFmpegPath              string `json:"ffmpegPath"`
	SnapshotPath            string `json:"snapshotPath"`
	Model                   string `json:"model"`
	ModelFamily             string `json:"modelFamily"`
	UpstreamProvider        string `json:"upstreamProvider"`
	UpstreamProviderSlug    string `json:"upstreamProviderSlug"`
	ReviewerID              string `json:"reviewerId"`
	PerRequestTimeoutMS     int64  `json:"perRequestTimeoutMs"`
	MaxChargeNanoUSD        int64  `json:"maxChargeNanoUsd"`
	ReasoningEnabled        bool   `json:"reasoningEnabled"`
}

type summary struct {
	SchemaVersion    int    `json:"schemaVersion"`
	Kind             string `json:"kind"`
	ResultSHA256     string `json:"resultSha256"`
	Outcome          string `json:"outcome"`
	Coverage         string `json:"coverage"`
	MatchCount       int    `json:"matchCount"`
	Requests         int    `json:"requests"`
	ChargedNanoUSD   int64  `json:"chargedNanoUsd"`
	TruthCreated     bool   `json:"truthCreated"`
	AdmissionAllowed bool   `json:"admissionAllowed"`
}

func main() {
	requestPath := flag.String("request", "", "absolute path to the private OpenRouter review request")
	outputDir := flag.String("output", "", "absolute path for the new private OpenRouter review evidence")
	flag.Parse()
	if err := run(*requestPath, *outputDir, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(requestPath, outputDir string, stdout io.Writer) error {
	if !cleanAbsolute(requestPath) || !cleanAbsolute(outputDir) || requestPath == outputDir {
		return errors.New("visual OpenRouter review requires distinct absolute request and output paths")
	}
	value, err := readPrivateJSON[request](requestPath)
	if err != nil {
		return err
	}
	if !cleanAbsolute(value.SnapshotPath) {
		return errors.New("visual OpenRouter review snapshot path is invalid")
	}
	snapshot, err := readPrivateJSON[fillerbakeoff.OpenRouterSnapshot](value.SnapshotPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := fillervisualsafety.RunCandidateBlindOpenRouterReview(ctx, fillervisualsafety.CandidateBlindOpenRouterConfig{
		BundlePath: value.BundlePath, ExpectedPackageSHA256: value.ExpectedPackageSHA256,
		ExpectedOwnerMapSHA256: value.ExpectedOwnerMapSHA256, ExpectedSelectionOrigin: value.ExpectedSelectionOrigin,
		OutputDir: outputDir, FFmpegPath: value.FFmpegPath,
		APIKey: os.Getenv("OPENROUTER_API_KEY"), Snapshot: snapshot,
		Model: value.Model, ModelFamily: value.ModelFamily, UpstreamProvider: value.UpstreamProvider,
		UpstreamProviderSlug: value.UpstreamProviderSlug, ReviewerID: value.ReviewerID,
		PerRequestTimeout: time.Duration(value.PerRequestTimeoutMS) * time.Millisecond,
		MaxChargeNanoUSD:  value.MaxChargeNanoUSD, ReasoningEnabled: value.ReasoningEnabled,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary{
		SchemaVersion: 1, Kind: "loomarr-visual-candidate-blind-openrouter-summary-v1",
		ResultSHA256: result.SHA256, Outcome: result.Assessment.Outcome,
		Coverage: result.Assessment.CoverageAssessment, MatchCount: len(result.Assessment.Matches),
		Requests: result.MaxRequests, ChargedNanoUSD: result.Attempt.ChargedNanoUSD,
		TruthCreated: false, AdmissionAllowed: false,
	})
}

func readPrivateJSON[T any](path string) (T, error) {
	var zero T
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maximumRequestBytes {
		return zero, errors.New("visual OpenRouter review input is not a bounded private regular file")
	}
	file, err := os.Open(path) //nolint:gosec // exact private input path validated above
	if err != nil {
		return zero, errors.New("visual OpenRouter review input cannot be opened")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximumRequestBytes+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return zero, errors.New("visual OpenRouter review input bytes drifted")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, errors.New("visual OpenRouter review input is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return zero, errors.New("visual OpenRouter review input has trailing content")
	}
	return value, nil
}

func cleanAbsolute(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}
