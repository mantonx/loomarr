package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func PublishTemporalStructureCompleteFamilyResult(outputPath, manifestPath string, result TemporalStructureCompleteFamilyResult) (string, error) {
	if strings.TrimSpace(outputPath) == "" || strings.TrimSpace(manifestPath) == "" {
		return "", fmt.Errorf("complete family publication requires output and public manifest paths")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(manifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return "", err
	}
	if err := validateTemporalStructureCompleteFamilyResultAgainstManifest(result, manifest, manifestSHA); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(outputPath, raw, 0o600); err != nil {
		return "", fmt.Errorf("publish complete family result: %w", err)
	}
	return hashBytes(raw), nil
}

func LoadTemporalStructureCompleteFamilyResult(path, manifestPath string) (TemporalStructureCompleteFamilyResult, string, error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(manifestPath) == "" {
		return TemporalStructureCompleteFamilyResult{}, "", fmt.Errorf("complete family load requires result and public manifest paths")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalStructureCompleteFamilyResult{}, "", fmt.Errorf("read complete family result: %w", err)
	}
	result, err := readStrictJSON[TemporalStructureCompleteFamilyResult](path)
	if err != nil {
		return TemporalStructureCompleteFamilyResult{}, "", fmt.Errorf("decode complete family result: %w", err)
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(manifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureCompleteFamilyResult{}, "", err
	}
	if err := validateTemporalStructureCompleteFamilyResultAgainstManifest(result, manifest, manifestSHA); err != nil {
		return TemporalStructureCompleteFamilyResult{}, "", err
	}
	return result, hashBytes(raw), nil
}
