package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// PublishTemporalStructureWindowFamilyResult validates the family answer against the exact public
// manifest before atomically creating a private, immutable result file.
func PublishTemporalStructureWindowFamilyResult(outputPath, manifestPath string, result TemporalStructureWindowFamilyResult) (string, error) {
	if strings.TrimSpace(outputPath) == "" || strings.TrimSpace(manifestPath) == "" {
		return "", fmt.Errorf("window family publication requires output and public manifest paths")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(manifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return "", err
	}
	if err := validateTemporalStructureWindowFamilyResultAgainstManifest(result, manifest, manifestSHA); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(outputPath, raw, 0o600); err != nil {
		return "", fmt.Errorf("publish window family result: %w", err)
	}
	return hashBytes(raw), nil
}

// LoadTemporalStructureWindowFamilyResult replays both the result and its binding to the exact
// public manifest. It does not open any private truth.
func LoadTemporalStructureWindowFamilyResult(path, manifestPath string) (TemporalStructureWindowFamilyResult, string, error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(manifestPath) == "" {
		return TemporalStructureWindowFamilyResult{}, "", fmt.Errorf("window family load requires result and public manifest paths")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalStructureWindowFamilyResult{}, "", fmt.Errorf("read window family result: %w", err)
	}
	result, err := readStrictJSON[TemporalStructureWindowFamilyResult](path)
	if err != nil {
		return TemporalStructureWindowFamilyResult{}, "", fmt.Errorf("decode window family result: %w", err)
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(manifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureWindowFamilyResult{}, "", err
	}
	if err := validateTemporalStructureWindowFamilyResultAgainstManifest(result, manifest, manifestSHA); err != nil {
		return TemporalStructureWindowFamilyResult{}, "", err
	}
	return result, hashBytes(raw), nil
}
