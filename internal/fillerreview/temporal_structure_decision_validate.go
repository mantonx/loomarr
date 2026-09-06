package fillerreview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// loadTemporalStructureDecisionArtifact proves that an artifact is the exact
// canonical result of the current reducer before certification opens truth.
func loadTemporalStructureDecisionArtifact(path, publicManifestPath string, assessmentPaths []string, expectedCases int) (TemporalStructureDecisionReport, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalStructureDecisionReport{}, "", fmt.Errorf("read temporal structure decision: %w", err)
	}
	report, err := readStrictJSON[TemporalStructureDecisionReport](path)
	if err != nil {
		return TemporalStructureDecisionReport{}, "", fmt.Errorf("decode temporal structure decision: %w", err)
	}
	if report.SchemaVersion != TemporalStructureDecisionSchemaVersion || report.ContractVersion != TemporalStructureDecisionContractVersion || report.BoundaryToleranceMS != TemporalStructureNearBoundaryMS || !reviewSHA256(report.AssessmentMediaProfileSHA256) || report.Cases != expectedCases || report.ProductionAdmissionAllowed {
		return TemporalStructureDecisionReport{}, "", fmt.Errorf("temporal structure decision identity, policy, count, or production disposition is invalid")
	}
	loaded, err := loadTemporalStructureDecision(TemporalStructureDecisionConfig{
		PublicManifestPath: publicManifestPath, PrivateAuthoritySHA256: report.PrivateAuthoritySHA256,
		AssessmentPaths: assessmentPaths, ExpectedCases: expectedCases, DecidedAt: report.DecidedAt,
	})
	if err != nil {
		return TemporalStructureDecisionReport{}, "", err
	}
	if report.AssessmentMediaProfileSHA256 != loaded.manifest.AssessmentMediaProfileSHA256 {
		return TemporalStructureDecisionReport{}, "", fmt.Errorf("temporal structure decision media profile does not bind the public challenge")
	}
	reproduced := buildTemporalStructureDecision(loaded, report.DecidedAt.UTC())
	reproducedRaw, err := json.MarshalIndent(reproduced, "", "  ")
	if err != nil {
		return TemporalStructureDecisionReport{}, "", err
	}
	reproducedRaw = append(reproducedRaw, '\n')
	if !bytes.Equal(raw, reproducedRaw) {
		return TemporalStructureDecisionReport{}, "", fmt.Errorf("temporal structure decision does not match deterministic reduction")
	}
	return report, hashBytes(raw), nil
}
