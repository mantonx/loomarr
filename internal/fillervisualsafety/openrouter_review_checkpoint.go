package fillervisualsafety

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	candidateBlindOpenRouterCheckpointName = "attempt.json"
	candidateBlindOpenRouterRawName        = "raw-response.json"
	candidateBlindOpenRouterResultName     = "result.json"
	maximumCandidateBlindCheckpointBytes   = int64(8 << 20)
)

type candidateBlindOpenRouterCheckpoint struct {
	SchemaVersion            int                             `json:"schemaVersion"`
	ReviewPackageSHA256      string                          `json:"reviewPackageSha256"`
	OwnerMapSHA256           string                          `json:"ownerMapSha256"`
	SelectionOrigin          string                          `json:"selectionOrigin"`
	CapabilitySnapshotSHA256 string                          `json:"capabilitySnapshotSha256"`
	Model                    string                          `json:"model"`
	ModelFamily              string                          `json:"modelFamily"`
	ResolvedModel            string                          `json:"resolvedModel"`
	UpstreamProvider         string                          `json:"upstreamProvider"`
	UpstreamProviderSlug     string                          `json:"upstreamProviderSlug"`
	ReviewerID               string                          `json:"reviewerId"`
	PromptVersion            string                          `json:"promptVersion"`
	PromptSHA256             string                          `json:"promptSha256"`
	SchemaSHA256             string                          `json:"schemaSha256"`
	ReasoningEnabled         bool                            `json:"reasoningEnabled"`
	MaxRequests              int                             `json:"maxRequests"`
	MaxChargeNanoUSD         int64                           `json:"maxChargeNanoUsd"`
	Input                    CandidateBlindHostedInput       `json:"input"`
	Attempt                  CandidateBlindOpenRouterAttempt `json:"attempt"`
}

func persistCandidateBlindOpenRouterCheckpoint(root string, checkpoint candidateBlindOpenRouterCheckpoint) error {
	raw, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if int64(len(raw)) > maximumCandidateBlindCheckpointBytes {
		return errors.New("candidate-blind OpenRouter checkpoint exceeds its byte ceiling")
	}
	temporary, err := os.CreateTemp(root, ".attempt-*")
	if err != nil {
		return errors.New("create candidate-blind OpenRouter checkpoint")
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.New("protect candidate-blind OpenRouter checkpoint")
	}
	writeErr := error(nil)
	if _, err := temporary.Write(raw); err != nil {
		writeErr = err
	} else if err := temporary.Sync(); err != nil {
		writeErr = err
	}
	if err := temporary.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		return errors.New("write candidate-blind OpenRouter checkpoint")
	}
	if err := os.Rename(temporaryPath, filepath.Join(root, candidateBlindOpenRouterCheckpointName)); err != nil {
		return errors.New("publish candidate-blind OpenRouter checkpoint")
	}
	return syncReviewDirectory(root)
}

func writeCandidateBlindOpenRouterRaw(root string, raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("candidate-blind OpenRouter raw response is empty")
	}
	path := filepath.Join(root, candidateBlindOpenRouterRawName)
	if err := writeReviewFile(path, raw); err != nil {
		return "", fmt.Errorf("persist candidate-blind OpenRouter raw response: %w", err)
	}
	return candidateBlindOpenRouterRawName, nil
}
