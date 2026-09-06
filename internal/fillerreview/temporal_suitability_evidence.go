package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type temporalSuitabilityEvidenceHeader struct {
	ContractVersion string `json:"contractVersion"`
}

// loadTemporalSuitabilityEvidence is the sole evidence-shape adapter for the
// direct-video screen. The paid transport consumes one narrow case surface;
// each source format retains its own strict authority loader.
func loadTemporalSuitabilityEvidence(manifestPath, structureAuthorityPath string) (TemporalTruthEvidenceManifest, string, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return TemporalTruthEvidenceManifest{}, "", fmt.Errorf("read suitability evidence header: %w", err)
	}
	var header temporalSuitabilityEvidenceHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return TemporalTruthEvidenceManifest{}, "", fmt.Errorf("decode suitability evidence header: %w", err)
	}
	switch header.ContractVersion {
	case TemporalTruthEvidenceContractVersion:
		if strings.TrimSpace(structureAuthorityPath) != "" {
			return TemporalTruthEvidenceManifest{}, "", fmt.Errorf("temporal truth evidence cannot carry structure authority")
		}
		return LoadTemporalTruthEvidence(manifestPath)
	case TemporalStructureChallengeContractVersion:
		if strings.TrimSpace(structureAuthorityPath) == "" {
			return TemporalTruthEvidenceManifest{}, "", fmt.Errorf("temporal structure suitability evidence requires private construction authority")
		}
		public, err := readStrictJSON[TemporalStructureChallengeManifest](manifestPath)
		if err != nil {
			return TemporalTruthEvidenceManifest{}, "", fmt.Errorf("read temporal structure suitability manifest: %w", err)
		}
		manifest, _, manifestSHA, authoritySHA, err := LoadTemporalStructureChallenge(manifestPath, structureAuthorityPath, len(public.Cases))
		if err != nil {
			return TemporalTruthEvidenceManifest{}, "", err
		}
		return temporalStructureChallengeSuitabilityEvidence(manifest, authoritySHA), manifestSHA, nil
	default:
		return TemporalTruthEvidenceManifest{}, "", fmt.Errorf("unsupported suitability evidence contract %q", header.ContractVersion)
	}
}

func temporalStructureChallengeSuitabilityEvidence(manifest TemporalStructureChallengeManifest, authoritySHA string) TemporalTruthEvidenceManifest {
	projected := TemporalTruthEvidenceManifest{SchemaVersion: TemporalTruthEvidenceSchemaVersion, ContractVersion: TemporalTruthEvidenceContractVersion, EvidenceVersion: manifest.ChallengeID, GeneratedAt: manifest.GeneratedAt, SelectionSHA256: authoritySHA, Cases: make([]TemporalTruthEvidenceCase, 0, len(manifest.Cases))}
	for _, item := range manifest.Cases {
		projected.Cases = append(projected.Cases, TemporalTruthEvidenceCase{Alias: item.Alias, DurationMS: item.Video.DurationMS, Video: item.Video})
	}
	return projected
}
