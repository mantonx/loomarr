package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalStructureCertificationSchemaVersion   = 4
	TemporalStructureCertificationContractVersion = "filler-temporal-structure-certification-v4"
	TemporalStructureCertificationPassed          = "passed"
	TemporalStructureCertificationFailed          = "failed"

	temporalStructureCertificationMinimumCases          = TemporalStructureHoldoutCases
	temporalStructureCertificationMinimumDecidedCases   = 30
	temporalStructureCertificationMinimumUnitDecisions  = 6
	temporalStructureCertificationMinimumSliceDecisions = 6
)

var temporalStructureCertificationRequiredSlices = []string{
	TemporalStructureSliceAdjacentSameRole,
	TemporalStructureSliceMixedRoleJoins,
	TemporalStructureSliceProgrammeNearEnd,
	TemporalStructureSliceProgrammeNearStart,
	TemporalStructureSliceSpotEarly,
	TemporalStructureSliceSpotLate,
	TemporalStructureSliceThreeItemCompilation,
	TemporalStructureSliceTwoItemCompilation,
}

type TemporalStructureCertificationConfig struct {
	HoldoutAuthoringPath string
	HoldoutReceiptPath   string
	PublicManifestPath   string
	PrivateAuthorityPath string
	DecisionPath         string
	AssessmentPaths      []string
	CertifiedAt          time.Time
	OutputPath           string
}

type TemporalStructureCertificationReport struct {
	SchemaVersion                int                                   `json:"schemaVersion"`
	ContractVersion              string                                `json:"contractVersion"`
	CertifiedAt                  time.Time                             `json:"certifiedAt"`
	ChallengeID                  string                                `json:"challengeId"`
	HoldoutAuthoringSHA256       string                                `json:"holdoutAuthoringSha256"`
	HoldoutReceiptSHA256         string                                `json:"holdoutReceiptSha256"`
	PublicManifestSHA256         string                                `json:"publicManifestSha256"`
	PrivateAuthoritySHA256       string                                `json:"privateAuthoritySha256"`
	AssessmentMediaProfileSHA256 string                                `json:"assessmentMediaProfileSha256"`
	MinimumTimelineDurationMS    int64                                 `json:"minimumTimelineDurationMs"`
	MaximumTimelineDurationMS    int64                                 `json:"maximumTimelineDurationMs"`
	MaximumAssessmentMediaBytes  int64                                 `json:"maximumAssessmentMediaBytes"`
	DecisionSHA256               string                                `json:"decisionSha256"`
	Cases                        int                                   `json:"cases"`
	DecidedCases                 int                                   `json:"decidedCases"`
	HeldCases                    int                                   `json:"heldCases"`
	WrongAutomaticDecisions      int                                   `json:"wrongAutomaticDecisions"`
	AssessorIDs                  []string                              `json:"assessorIds"`
	ModelFamilies                []string                              `json:"modelFamilies"`
	BoundaryToleranceMS          int64                                 `json:"boundaryToleranceMs"`
	MinimumDecidedCases          int                                   `json:"minimumDecidedCases"`
	MinimumUnitDecisions         int                                   `json:"minimumUnitDecisions"`
	MinimumSliceDecisions        int                                   `json:"minimumSliceDecisions"`
	Units                        []TemporalStructureUnitCertification  `json:"units"`
	Slices                       []TemporalStructureSliceCertification `json:"slices"`
	CertifiedUnits               []fillereval.UnitKind                 `json:"certifiedUnits"`
	CertifiedSlices              []string                              `json:"certifiedSlices"`
	FailureCodes                 []string                              `json:"failureCodes,omitempty"`
	CertificationStatus          string                                `json:"certificationStatus"`
	TrainingAllowed              bool                                  `json:"trainingAllowed"`
	ProductionAdmissionAllowed   bool                                  `json:"productionAdmissionAllowed"`
	NextAction                   string                                `json:"nextAction"`
}

type TemporalStructureUnitCertification struct {
	Unit                    fillereval.UnitKind `json:"unit"`
	Cases                   int                 `json:"cases"`
	DecidedCases            int                 `json:"decidedCases"`
	HeldCases               int                 `json:"heldCases"`
	WrongAutomaticDecisions int                 `json:"wrongAutomaticDecisions"`
	FailureCodes            []string            `json:"failureCodes,omitempty"`
	Passed                  bool                `json:"passed"`
}

type TemporalStructureSliceCertification struct {
	Slice                   string   `json:"slice"`
	Cases                   int      `json:"cases"`
	DecidedCases            int      `json:"decidedCases"`
	HeldCases               int      `json:"heldCases"`
	WrongAutomaticDecisions int      `json:"wrongAutomaticDecisions"`
	FailureCodes            []string `json:"failureCodes,omitempty"`
	Passed                  bool     `json:"passed"`
}

// PublishTemporalStructureCertification first reproduces the immutable,
// truth-blind decision artifact. Only then does it open construction truth and
// score the reducer's automatic decisions; raw assessor errors are not gates.
func PublishTemporalStructureCertification(config TemporalStructureCertificationConfig) (TemporalStructureCertificationReport, string, error) {
	paths := []string{
		config.HoldoutAuthoringPath, config.HoldoutReceiptPath, config.PublicManifestPath,
		config.PrivateAuthorityPath, config.DecisionPath, config.OutputPath,
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return TemporalStructureCertificationReport{}, "", fmt.Errorf("temporal structure certification requires every authority, decision, and output path")
		}
	}
	if len(config.AssessmentPaths) < 2 || config.CertifiedAt.IsZero() {
		return TemporalStructureCertificationReport{}, "", fmt.Errorf("temporal structure certification requires at least two assessments and a certification time")
	}

	decision, decisionSHA, err := loadTemporalStructureDecisionArtifact(
		config.DecisionPath, config.PublicManifestPath, config.AssessmentPaths, TemporalStructureHoldoutCases,
	)
	if err != nil {
		return TemporalStructureCertificationReport{}, "", err
	}
	if config.CertifiedAt.Before(decision.DecidedAt) {
		return TemporalStructureCertificationReport{}, "", fmt.Errorf("temporal structure certification predates the decision artifact")
	}

	authoringRaw, receiptRaw, receipt, err := loadTemporalStructureCertificationHoldout(config)
	if err != nil {
		return TemporalStructureCertificationReport{}, "", err
	}
	manifest, authority, publicSHA, authoritySHA, err := LoadTemporalStructureChallenge(
		config.PublicManifestPath, config.PrivateAuthorityPath, TemporalStructureHoldoutCases,
	)
	if err != nil {
		return TemporalStructureCertificationReport{}, "", err
	}
	if decision.ChallengeID != manifest.ChallengeID || decision.PublicManifestSHA256 != publicSHA || decision.PrivateAuthoritySHA256 != authoritySHA {
		return TemporalStructureCertificationReport{}, "", fmt.Errorf("temporal structure decision does not bind the challenge authority")
	}
	if decision.AssessmentMediaProfileSHA256 != authority.AssessmentMediaProfile.SHA256 {
		return TemporalStructureCertificationReport{}, "", fmt.Errorf("temporal structure decision does not bind the challenge media profile")
	}
	if authority.AuthoringSHA256 != receipt.AuthoringSHA256 || authority.SeedSHA256 != receipt.SeedSHA256 || authority.GeneratedAt.Before(receipt.PlannedAt) {
		return TemporalStructureCertificationReport{}, "", fmt.Errorf("temporal structure challenge does not descend from the certified holdout")
	}

	report := scoreTemporalStructureCertification(decision, manifest, authority, config.CertifiedAt)
	report.HoldoutAuthoringSHA256 = hashBytes(authoringRaw)
	report.HoldoutReceiptSHA256 = hashBytes(receiptRaw)
	report.PublicManifestSHA256 = publicSHA
	report.PrivateAuthoritySHA256 = authoritySHA
	report.DecisionSHA256 = decisionSHA
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return TemporalStructureCertificationReport{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureCertificationReport{}, "", fmt.Errorf("publish temporal structure certification: %w", err)
	}
	return report, hashBytes(raw), nil
}

func loadTemporalStructureCertificationHoldout(config TemporalStructureCertificationConfig) ([]byte, []byte, TemporalStructureHoldoutReceipt, error) {
	authoringRaw, err := os.ReadFile(config.HoldoutAuthoringPath)
	if err != nil {
		return nil, nil, TemporalStructureHoldoutReceipt{}, fmt.Errorf("read temporal structure holdout authoring: %w", err)
	}
	receiptRaw, err := os.ReadFile(config.HoldoutReceiptPath)
	if err != nil {
		return nil, nil, TemporalStructureHoldoutReceipt{}, fmt.Errorf("read temporal structure holdout receipt: %w", err)
	}
	authoring, err := readStrictJSON[TemporalStructureChallengeAuthoring](config.HoldoutAuthoringPath)
	if err != nil {
		return nil, nil, TemporalStructureHoldoutReceipt{}, fmt.Errorf("decode temporal structure holdout authoring: %w", err)
	}
	receipt, err := readStrictJSON[TemporalStructureHoldoutReceipt](config.HoldoutReceiptPath)
	if err != nil {
		return nil, nil, TemporalStructureHoldoutReceipt{}, fmt.Errorf("decode temporal structure holdout receipt: %w", err)
	}
	if hashBytes(authoringRaw) != receipt.AuthoringSHA256 {
		return nil, nil, TemporalStructureHoldoutReceipt{}, fmt.Errorf("temporal structure holdout receipt does not bind authoring bytes")
	}
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, nil); err != nil {
		return nil, nil, TemporalStructureHoldoutReceipt{}, err
	}
	return authoringRaw, receiptRaw, receipt, nil
}
