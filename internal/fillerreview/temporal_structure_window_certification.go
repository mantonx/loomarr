package fillerreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

const (
	TemporalStructureWindowCertificationSchemaVersion   = 1
	TemporalStructureWindowCertificationContractVersion = "filler-temporal-structure-window-certification-v1"
)

type TemporalStructureWindowCertificationConfig struct {
	SuitePath             string
	WindowSetManifestPath string
	FirstFamilyPath       string
	SecondFamilyPath      string
	CertifiedAt           time.Time
	OutputPath            string
}

type TemporalStructureWindowCertificationFamilyEvidence struct {
	Assessor         fillerstructure.AssessorProfile `json:"assessor"`
	ResultSHA256     string                          `json:"resultSha256"`
	ResultFileSHA256 string                          `json:"resultFileSha256"`
}

// TemporalStructureWindowCertificationArtifact binds the scored report to both immutable,
// truth-blind family artifacts and the exact private suite used to score them.
type TemporalStructureWindowCertificationArtifact struct {
	SchemaVersion                   int                                                  `json:"schemaVersion"`
	ContractVersion                 string                                               `json:"contractVersion"`
	WindowSetManifestSHA256         string                                               `json:"windowSetManifestSha256"`
	SuiteSHA256                     string                                               `json:"suiteSha256"`
	SuiteFileSHA256                 string                                               `json:"suiteFileSha256"`
	Families                        []TemporalStructureWindowCertificationFamilyEvidence `json:"families"`
	Report                          fillerstructurewindowcert.Report                     `json:"report"`
	TrainingAllowed                 bool                                                 `json:"trainingAllowed"`
	AutomaticMaterializationAllowed bool                                                 `json:"automaticMaterializationAllowed"`
	SHA256                          string                                               `json:"sha256"`
}

// PublishTemporalStructureWindowCertification opens private truth only after independently
// loading both complete blinded family artifacts, joins by media-set identity, scores, and writes
// one immutable evidence-bound report.
func PublishTemporalStructureWindowCertification(config TemporalStructureWindowCertificationConfig) (TemporalStructureWindowCertificationArtifact, string, error) {
	paths := []string{config.SuitePath, config.WindowSetManifestPath, config.FirstFamilyPath, config.SecondFamilyPath, config.OutputPath}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return TemporalStructureWindowCertificationArtifact{}, "", errors.New("window certification requires suite, manifest, two family results, and output paths")
		}
	}
	if config.CertifiedAt.IsZero() || config.CertifiedAt != config.CertifiedAt.UTC() {
		return TemporalStructureWindowCertificationArtifact{}, "", errors.New("window certification requires canonical UTC certification time")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", err
	}
	first, firstFileSHA, err := LoadTemporalStructureWindowFamilyResult(config.FirstFamilyPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", fmt.Errorf("load first window family: %w", err)
	}
	second, secondFileSHA, err := LoadTemporalStructureWindowFamilyResult(config.SecondFamilyPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", fmt.Errorf("load second window family: %w", err)
	}
	if config.CertifiedAt.Before(first.CompletedAt) || config.CertifiedAt.Before(second.CompletedAt) {
		return TemporalStructureWindowCertificationArtifact{}, "", errors.New("window certification predates a family result")
	}
	suite, suiteFileSHA, err := LoadTemporalStructureWindowCertificationSuite(config.SuitePath)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", fmt.Errorf("load private window certification suite: %w", err)
	}
	results, err := bindTemporalStructureWindowCertificationCases(manifest, suite, first, second)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", err
	}
	report, err := fillerstructurewindowcert.Certify(suite, results, config.CertifiedAt)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", err
	}
	families := []TemporalStructureWindowCertificationFamilyEvidence{
		{Assessor: first.Assessor, ResultSHA256: first.SHA256, ResultFileSHA256: firstFileSHA},
		{Assessor: second.Assessor, ResultSHA256: second.SHA256, ResultFileSHA256: secondFileSHA},
	}
	slices.SortFunc(families, func(left, right TemporalStructureWindowCertificationFamilyEvidence) int {
		return strings.Compare(left.Assessor.ID, right.Assessor.ID)
	})
	artifact := TemporalStructureWindowCertificationArtifact{
		SchemaVersion: TemporalStructureWindowCertificationSchemaVersion, ContractVersion: TemporalStructureWindowCertificationContractVersion,
		WindowSetManifestSHA256: manifestSHA, SuiteSHA256: suite.SHA256, SuiteFileSHA256: suiteFileSHA,
		Families: families, Report: report,
	}
	artifact.SHA256 = temporalStructureWindowCertificationSHA256(artifact)
	if err := ValidateTemporalStructureWindowCertificationArtifact(artifact); err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", err
	}
	raw, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", fmt.Errorf("publish window certification: %w", err)
	}
	return artifact, hashBytes(raw), nil
}

func temporalStructureWindowCertificationSHA256(artifact TemporalStructureWindowCertificationArtifact) string {
	artifact.SHA256 = ""
	return temporalTruthJSONSHA(artifact)
}
