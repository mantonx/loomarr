// Command reviewbundle creates one private candidate-output-blind visual
// review bundle. It performs no model call and grants no truth or admission.
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

const maximumRequestBytes = int64(2 << 20)

type request struct {
	Alias           string                             `json:"alias"`
	SourceFamilyID  string                             `json:"sourceFamilyId"`
	RightsSHA256    string                             `json:"rightsSha256"`
	SelectionOrigin string                             `json:"selectionOrigin"`
	SourceAuthority fillervisualsafety.SourceAuthority `json:"sourceAuthority"`
	SourcePath      string                             `json:"sourcePath"`
	CoverageProfile fillervisualsafety.CoverageProfile `json:"coverageProfile"`
	PolicyPath      string                             `json:"policyPath"`
	FFmpegPath      string                             `json:"ffmpegPath"`
	PreparedAt      time.Time                          `json:"preparedAt"`
}

type summary struct {
	SchemaVersion    int    `json:"schemaVersion"`
	Kind             string `json:"kind"`
	PackageSHA256    string `json:"packageSha256"`
	OwnerMapSHA256   string `json:"ownerMapSha256"`
	FrameCount       int    `json:"frameCount"`
	TruthCreated     bool   `json:"truthCreated"`
	AdmissionAllowed bool   `json:"admissionAllowed"`
}

func main() {
	requestPath := flag.String("request", "", "absolute path to the private review-bundle request")
	outputDir := flag.String("output", "", "absolute path for the new private review bundle")
	flag.Parse()
	if err := run(*requestPath, *outputDir, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(requestPath, outputDir string, stdout io.Writer) error {
	if !cleanAbsolute(requestPath) || !cleanAbsolute(outputDir) || requestPath == outputDir {
		return errors.New("visual review bundle requires distinct absolute request and output paths")
	}
	value, err := readRequest(requestPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := fillervisualsafety.BuildCandidateBlindReviewBundle(ctx, fillervisualsafety.CandidateBlindReviewConfig{
		Alias: value.Alias, SourceFamilyID: value.SourceFamilyID, RightsSHA256: value.RightsSHA256,
		SelectionOrigin: value.SelectionOrigin,
		Source:          fillervisualsafety.SourceRequest{Authority: value.SourceAuthority, Path: value.SourcePath},
		Profile:         value.CoverageProfile, PolicyPath: value.PolicyPath,
		FFmpegPath: value.FFmpegPath, OutputDir: outputDir,
		PreparedAt: value.PreparedAt,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary{
		SchemaVersion: 1, Kind: "loomarr-visual-candidate-blind-review-summary-v1",
		PackageSHA256: result.PackageSHA256, OwnerMapSHA256: result.OwnerMapSHA256,
		FrameCount: result.FrameCount, TruthCreated: false, AdmissionAllowed: false,
	})
}

func readRequest(path string) (request, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maximumRequestBytes {
		return request{}, errors.New("visual review bundle request is not a bounded private regular file")
	}
	file, err := os.Open(path) //nolint:gosec // absolute private request validated above
	if err != nil {
		return request{}, errors.New("visual review bundle request cannot be opened")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximumRequestBytes+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return request{}, errors.New("visual review bundle request bytes drifted")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value request
	if err := decoder.Decode(&value); err != nil {
		return request{}, errors.New("visual review bundle request is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return request{}, errors.New("visual review bundle request has trailing content")
	}
	return value, nil
}

func cleanAbsolute(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}
