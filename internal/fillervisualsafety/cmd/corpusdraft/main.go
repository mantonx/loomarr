// Command corpusdraft prepares one private, candidate-model-blind visual
// corpus review package. It grants no truth, training, or admission authority.
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

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

const maximumCorpusDraftRequestBytes = int64(8 << 20)

type request struct {
	Authority     fillervisualsafety.VisualCorpusDraftAuthority `json:"authority"`
	SourceRoot    string                                        `json:"sourceRoot"`
	PolicyPath    string                                        `json:"policyPath"`
	AliasSeedPath string                                        `json:"aliasSeedPath"`
	PreparedAt    time.Time                                     `json:"preparedAt"`
}

type summary struct {
	SchemaVersion              int    `json:"schemaVersion"`
	Kind                       string `json:"kind"`
	ManifestSHA256             string `json:"manifestSha256"`
	OwnerMapSHA256             string `json:"ownerMapSha256"`
	CaseCount                  int    `json:"caseCount"`
	CandidateModelOutput       bool   `json:"candidateModelOutput"`
	TruthAuthorityCreated      bool   `json:"truthAuthorityCreated"`
	TrainingAllowed            bool   `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool   `json:"productionAdmissionAllowed"`
}

func main() {
	requestPath := flag.String("request", "", "absolute path to the private corpus-draft request")
	outputDir := flag.String("output", "", "absolute path for the new private corpus draft")
	flag.Parse()
	if err := run(*requestPath, *outputDir, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(requestPath, outputDir string, stdout io.Writer) error {
	if !cleanAbsolute(requestPath) || !cleanAbsolute(outputDir) || requestPath == outputDir {
		return errors.New("visual corpus draft requires distinct absolute request and output paths")
	}
	value, err := readRequest(requestPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := fillervisualsafety.PrepareVisualCorpusDraft(ctx, fillervisualsafety.VisualCorpusDraftConfig{
		Authority: value.Authority, SourceRoot: value.SourceRoot, PolicyPath: value.PolicyPath,
		AliasSeedPath: value.AliasSeedPath, OutputDir: outputDir, PreparedAt: value.PreparedAt,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary{
		SchemaVersion: 1, Kind: "loomarr-visual-corpus-draft-summary-v1",
		ManifestSHA256: result.ManifestSHA256, OwnerMapSHA256: result.OwnerMapSHA256,
		CaseCount: result.CaseCount, CandidateModelOutput: false, TruthAuthorityCreated: false,
		TrainingAllowed: false, ProductionAdmissionAllowed: false,
	})
}

func readRequest(path string) (request, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maximumCorpusDraftRequestBytes {
		return request{}, errors.New("visual corpus draft request is not a bounded private regular file")
	}
	file, err := os.Open(path) //nolint:gosec // absolute private request validated above
	if err != nil {
		return request{}, errors.New("visual corpus draft request cannot be opened")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximumCorpusDraftRequestBytes+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return request{}, errors.New("visual corpus draft request bytes drifted")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value request
	if err := decoder.Decode(&value); err != nil {
		return request{}, errors.New("visual corpus draft request is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return request{}, errors.New("visual corpus draft request has trailing content")
	}
	return value, nil
}

func cleanAbsolute(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}
