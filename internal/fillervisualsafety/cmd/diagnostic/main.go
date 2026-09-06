// Command diagnostic runs one explicitly configured, development-only
// complete-source portable visual inference. It grants no policy verdict.
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

const maximumDiagnosticRequestBytes = int64(2 << 20)

type diagnosticRequest struct {
	SourceAuthority fillervisualsafety.SourceAuthority    `json:"sourceAuthority"`
	SourcePath      string                                `json:"sourcePath"`
	CoverageProfile fillervisualsafety.CoverageProfile    `json:"coverageProfile"`
	Capability      fillervisualsafety.PortableCapability `json:"capability"`
	WorkerPath      string                                `json:"workerPath"`
	FFmpegPath      string                                `json:"ffmpegPath"`
}

type diagnosticReport struct {
	SchemaVersion              int                                          `json:"schemaVersion"`
	Kind                       string                                       `json:"kind"`
	StartedAt                  time.Time                                    `json:"startedAt"`
	CompletedAt                time.Time                                    `json:"completedAt"`
	SourceAuthoritySHA256      string                                       `json:"sourceAuthoritySha256"`
	CoverageProfileSHA256      string                                       `json:"coverageProfileSha256"`
	CapabilitySHA256           string                                       `json:"capabilitySha256"`
	Coverage                   fillervisualsafety.CoverageEvidence          `json:"coverage"`
	Inference                  fillervisualsafety.PortableInferenceEvidence `json:"inference"`
	ProductionAdmissionAllowed bool                                         `json:"productionAdmissionAllowed"`
	TrainingAllowed            bool                                         `json:"trainingAllowed"`
}

func main() {
	requestPath := flag.String("request", "", "absolute path to the private diagnostic request")
	outputPath := flag.String("output", "", "absolute path for the new private diagnostic report")
	flag.Parse()
	if err := run(*requestPath, *outputPath); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(requestPath, outputPath string) error {
	if !cleanAbsolute(requestPath) || !cleanAbsolute(outputPath) || requestPath == outputPath {
		return errors.New("visual diagnostic requires distinct absolute request and output paths")
	}
	request, err := readRequest(requestPath)
	if err != nil {
		return err
	}
	if err := validateRequest(request); err != nil {
		return err
	}
	if _, err := os.Lstat(outputPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("visual diagnostic output already exists or cannot be inspected")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	started := time.Now().UTC()
	prepared, err := fillervisualsafety.Prepare(ctx, fillervisualsafety.SourceRequest{
		Authority: request.SourceAuthority, Path: request.SourcePath,
	}, request.CoverageProfile)
	if err != nil {
		return fmt.Errorf("prepare visual diagnostic: %w", err)
	}
	defer func() { _ = prepared.Close() }()
	execution, err := fillervisualsafety.RunPortableCoverage(
		ctx, prepared, request.FFmpegPath, request.WorkerPath, request.Capability,
	)
	if err != nil {
		return fmt.Errorf("run visual diagnostic: %w", err)
	}
	report := diagnosticReport{
		SchemaVersion: 1, Kind: "loomarr-portable-visual-development-diagnostic-v1",
		StartedAt: started, CompletedAt: time.Now().UTC(),
		SourceAuthoritySHA256: request.SourceAuthority.SHA256,
		CoverageProfileSHA256: request.CoverageProfile.SHA256,
		CapabilitySHA256:      request.Capability.SHA256,
		Coverage:              execution.Coverage, Inference: execution.Inference,
		ProductionAdmissionAllowed: false, TrainingAllowed: false,
	}
	return writeReport(outputPath, report)
}

func readRequest(path string) (diagnosticRequest, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximumDiagnosticRequestBytes {
		return diagnosticRequest{}, errors.New("visual diagnostic request is not a bounded regular file")
	}
	file, err := os.Open(path) //nolint:gosec // absolute private request validated immediately above
	if err != nil {
		return diagnosticRequest{}, errors.New("visual diagnostic request cannot be opened")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximumDiagnosticRequestBytes+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return diagnosticRequest{}, errors.New("visual diagnostic request bytes drifted")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request diagnosticRequest
	if err := decoder.Decode(&request); err != nil {
		return diagnosticRequest{}, errors.New("visual diagnostic request is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return diagnosticRequest{}, errors.New("visual diagnostic request has trailing content")
	}
	return request, nil
}

func validateRequest(request diagnosticRequest) error {
	if fillervisualsafety.ValidateSourceAuthority(request.SourceAuthority) != nil ||
		fillervisualsafety.ValidateCoverageProfile(request.CoverageProfile) != nil ||
		fillervisualsafety.ValidatePortableCapability(request.Capability) != nil ||
		!cleanAbsolute(request.SourcePath) || !cleanAbsolute(request.WorkerPath) ||
		request.FFmpegPath == "" {
		return errors.New("visual diagnostic request authority is invalid")
	}
	return nil
}

func writeReport(path string, report diagnosticReport) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // caller selected a new private path
	if err != nil {
		return fmt.Errorf("create visual diagnostic report: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write visual diagnostic report: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close visual diagnostic report: %w", closeErr)
	}
	return nil
}

func cleanAbsolute(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}
