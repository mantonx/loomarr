package fillerreview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalStructureAnchorAdjudicationSchemaVersion      = 1
	TemporalStructureAnchorAdjudicationSubmissionContract = "filler-temporal-structure-anchor-adjudication-submission-v1"
	TemporalStructureAnchorAdjudicationAuthorityContract  = "filler-temporal-structure-anchor-adjudication-authority-v1"

	TemporalStructureAnchorReviewComplete = "complete_audiovisual"

	TemporalStructureAnchorConfirmed                  = "confirmed_original"
	TemporalStructureAnchorStructuralDisqualification = "structural_disqualification"
	TemporalStructureAnchorRoleCorrection             = "role_correction"

	TemporalStructureBurnedDiagnosticOnly = "burned_diagnostic_only"
)

type TemporalStructureAnchorAdjudicationConfig struct {
	PublicManifestPath   string
	PrivateAuthorityPath string
	PlanAuthoringPath    string
	PlanReceiptPath      string
	AssessmentPaths      []string
	ComparisonPath       string
	SubmissionPath       string
	ExpectedCases        int
	AdjudicatedAt        time.Time
	OutputPath           string
}

type TemporalStructureAnchorAdjudicationSubmission struct {
	SchemaVersion    int                                                 `json:"schemaVersion"`
	ContractVersion  string                                              `json:"contractVersion"`
	ChallengeID      string                                              `json:"challengeId"`
	ComparisonSHA256 string                                              `json:"comparisonSha256"`
	ReviewerID       string                                              `json:"reviewerId"`
	ReviewedAt       time.Time                                           `json:"reviewedAt"`
	Cases            []TemporalStructureAnchorAdjudicationSubmissionCase `json:"cases"`
}

type TemporalStructureAnchorAdjudicationSubmissionCase struct {
	Alias        string                              `json:"alias"`
	Coverage     string                              `json:"coverage"`
	Observations TemporalStructureAnchorObservations `json:"observations"`
	Disposition  string                              `json:"disposition"`
	Unit         fillereval.UnitKind                 `json:"unit"`
	Role         fillereval.TemporalRole             `json:"role,omitempty"`
	DecisiveAtMS []int64                             `json:"decisiveAtMs,omitempty"`
	Rationale    string                              `json:"rationale"`
}

type TemporalStructureAnchorObservations struct {
	Opening       string                                   `json:"opening"`
	InternalJoins []TemporalStructureAnchorJoinObservation `json:"internalJoins"`
	Closing       string                                   `json:"closing"`
}

type TemporalStructureAnchorJoinObservation struct {
	AtMS        int64  `json:"atMs"`
	Observation string `json:"observation"`
}

type TemporalStructureAnchorAdjudicationAuthority struct {
	SchemaVersion                   int                                       `json:"schemaVersion"`
	ContractVersion                 string                                    `json:"contractVersion"`
	ChallengeID                     string                                    `json:"challengeId"`
	AdjudicatedAt                   time.Time                                 `json:"adjudicatedAt"`
	ReviewerID                      string                                    `json:"reviewerId"`
	Inputs                          []TemporalStructureHoldoutInput           `json:"inputs"`
	EvidenceManifestSHA256          string                                    `json:"evidenceManifestSha256"`
	HumanAssessmentSHA256           string                                    `json:"humanAssessmentSha256"`
	PlanReceiptSHA256               string                                    `json:"planReceiptSha256"`
	ComparisonSHA256                string                                    `json:"comparisonSha256"`
	PriorExposure                   TemporalStructureHoldoutTrainingExclusion `json:"priorExposure"`
	Cases                           []TemporalStructureAnchorAdjudicationCase `json:"cases"`
	ChallengeDisposition            string                                    `json:"challengeDisposition"`
	BlindHumanAuditRequired         bool                                      `json:"blindHumanAuditRequired"`
	CertificationScoreRepairAllowed bool                                      `json:"certificationScoreRepairAllowed"`
	TrainingAllowed                 bool                                      `json:"trainingAllowed"`
	ProductionAdmissionAllowed      bool                                      `json:"productionAdmissionAllowed"`
}

type TemporalStructureAnchorAdjudicationCase struct {
	Alias         string                              `json:"alias"`
	EvidenceAlias string                              `json:"evidenceAlias"`
	CaseID        string                              `json:"caseId"`
	SourceID      string                              `json:"sourceId"`
	SourceSHA256  string                              `json:"sourceSha256"`
	FamilyID      string                              `json:"familyId"`
	DurationMS    int64                               `json:"durationMs"`
	Coverage      string                              `json:"coverage"`
	Observations  TemporalStructureAnchorObservations `json:"observations"`
	Disposition   string                              `json:"disposition"`
	Original      TemporalStructureTruthLabel         `json:"original"`
	Adjudicated   TemporalStructureTruthLabel         `json:"adjudicated"`
	DecisiveAtMS  []int64                             `json:"decisiveAtMs,omitempty"`
	Rationale     string                              `json:"rationale"`
}

type TemporalStructureAnchorAdjudicationResult struct {
	Cases           int
	AuthoritySHA256 string
}

// PublishTemporalStructureAnchorAdjudication reproduces the opened model
// comparison and joins only its standalone diagnostic targets to one complete-
// span review. It preserves every prior artifact and emits no corrected score.
func PublishTemporalStructureAnchorAdjudication(config TemporalStructureAnchorAdjudicationConfig) (TemporalStructureAnchorAdjudicationResult, error) {
	authority, err := buildTemporalStructureAnchorAdjudication(config)
	if err != nil {
		return TemporalStructureAnchorAdjudicationResult{}, err
	}
	raw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return TemporalStructureAnchorAdjudicationResult{}, err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureAnchorAdjudicationResult{}, fmt.Errorf("publish temporal structure anchor adjudication: %w", err)
	}
	return TemporalStructureAnchorAdjudicationResult{Cases: len(authority.Cases), AuthoritySHA256: hashBytes(raw)}, nil
}

func buildTemporalStructureAnchorAdjudication(config TemporalStructureAnchorAdjudicationConfig) (TemporalStructureAnchorAdjudicationAuthority, error) {
	if err := validateTemporalStructureAnchorAdjudicationConfig(config); err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, err
	}
	manifest, challenge, publicSHA, challengeSHA, err := LoadTemporalStructureChallenge(config.PublicManifestPath, config.PrivateAuthorityPath, config.ExpectedCases)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, err
	}
	authoringRaw, authoring, err := loadTemporalStructureChallengeAuthoring(config.PlanAuthoringPath)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, err
	}
	receiptRaw, err := os.ReadFile(config.PlanReceiptPath)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("read adjudication plan receipt: %w", err)
	}
	receipt, err := readStrictJSON[TemporalStructureHoldoutReceipt](config.PlanReceiptPath)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("decode adjudication plan receipt: %w", err)
	}
	if receipt.AuthoringSHA256 != hashBytes(authoringRaw) || challenge.AuthoringSHA256 != receipt.AuthoringSHA256 || challenge.PlanContractVersion != receipt.ContractVersion || challenge.PlanReceiptSHA256 != hashBytes(receiptRaw) {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("anchor adjudication challenge does not bind its plan")
	}
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, nil); err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("validate adjudication plan receipt: %w", err)
	}

	comparisonRaw, err := os.ReadFile(config.ComparisonPath)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("read anchor adjudication comparison: %w", err)
	}
	comparison, err := readStrictJSON[TemporalStructureComparisonReport](config.ComparisonPath)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("decode anchor adjudication comparison: %w", err)
	}
	reproduced, err := CompareTemporalStructureAssessments(TemporalStructureComparisonConfig{
		PublicManifestPath: config.PublicManifestPath, PrivateAuthorityPath: config.PrivateAuthorityPath,
		AssessmentPaths: config.AssessmentPaths, ExpectedCases: config.ExpectedCases, ComparedAt: comparison.ComparedAt,
	})
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("reproduce anchor adjudication comparison: %w", err)
	}
	reproducedRaw, err := json.MarshalIndent(reproduced, "", "  ")
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, err
	}
	reproducedRaw = append(reproducedRaw, '\n')
	if !bytes.Equal(comparisonRaw, reproducedRaw) || comparison.ChallengeID != manifest.ChallengeID || comparison.PublicManifestSHA256 != publicSHA || comparison.PrivateAuthoritySHA256 != challengeSHA {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("anchor adjudication comparison is not the exact reproduced challenge result")
	}
	comparisonSHA := hashBytes(comparisonRaw)

	submissionRaw, err := os.ReadFile(config.SubmissionPath)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("read anchor adjudication submission: %w", err)
	}
	submission, err := readStrictJSON[TemporalStructureAnchorAdjudicationSubmission](config.SubmissionPath)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("decode anchor adjudication submission: %w", err)
	}
	cases, err := validateTemporalStructureAnchorAdjudicationSubmission(submission, comparison, challenge, receipt, comparisonSHA, config.AdjudicatedAt)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, err
	}

	inputs := []TemporalStructureHoldoutInput{
		{Name: "comparison", SHA256: comparisonSHA},
		{Name: "plan_authoring", SHA256: hashBytes(authoringRaw)},
		{Name: "plan_receipt", SHA256: hashBytes(receiptRaw)},
		{Name: "private_authority", SHA256: challengeSHA},
		{Name: "public_manifest", SHA256: publicSHA},
		{Name: "submission", SHA256: hashBytes(submissionRaw)},
	}
	for _, input := range receipt.Inputs {
		inputs = append(inputs, TemporalStructureHoldoutInput{Name: "plan_input:" + input.Name, SHA256: input.SHA256})
	}
	for _, path := range config.AssessmentPaths {
		set, err := readStrictJSON[TemporalStructureAssessmentSet](path)
		if err != nil {
			return TemporalStructureAnchorAdjudicationAuthority{}, fmt.Errorf("decode adjudication assessment input: %w", err)
		}
		digest, err := hashFile(path)
		if err != nil {
			return TemporalStructureAnchorAdjudicationAuthority{}, err
		}
		inputs = append(inputs, TemporalStructureHoldoutInput{Name: "assessment_set:" + set.Assessor.ID, SHA256: digest})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })

	evidenceSHA, humanSHA, err := temporalStructureAdjudicationPlanInputs(receipt)
	if err != nil {
		return TemporalStructureAnchorAdjudicationAuthority{}, err
	}
	requestExposure := emptyTemporalStructureHoldoutExposure()
	for _, item := range manifest.Cases {
		requestExposure.SourceSHA256 = append(requestExposure.SourceSHA256, item.Video.SHA256)
	}
	priorExposure := unionTemporalStructureHoldoutExposure(receipt.FutureTrainingExclusion, requestExposure)
	return TemporalStructureAnchorAdjudicationAuthority{
		SchemaVersion: TemporalStructureAnchorAdjudicationSchemaVersion, ContractVersion: TemporalStructureAnchorAdjudicationAuthorityContract,
		ChallengeID: manifest.ChallengeID, AdjudicatedAt: config.AdjudicatedAt.UTC(), ReviewerID: submission.ReviewerID,
		Inputs: inputs, EvidenceManifestSHA256: evidenceSHA, HumanAssessmentSHA256: humanSHA,
		PlanReceiptSHA256: hashBytes(receiptRaw), ComparisonSHA256: comparisonSHA,
		PriorExposure: priorExposure, Cases: cases,
		ChallengeDisposition:    TemporalStructureBurnedDiagnosticOnly,
		BlindHumanAuditRequired: false, CertificationScoreRepairAllowed: false, TrainingAllowed: false, ProductionAdmissionAllowed: false,
	}, nil
}

func cloneTemporalStructureTrainingExclusion(value TemporalStructureHoldoutTrainingExclusion) TemporalStructureHoldoutTrainingExclusion {
	value.SourceSHA256 = slices.Clone(value.SourceSHA256)
	value.FamilyIDs = slices.Clone(value.FamilyIDs)
	value.ProgrammeProvenance = slices.Clone(value.ProgrammeProvenance)
	return value
}

func temporalStructureAdjudicationPlanInputs(receipt TemporalStructureHoldoutReceipt) (string, string, error) {
	var evidenceSHA, humanSHA string
	for _, input := range receipt.Inputs {
		switch input.Name {
		case "evidence_manifest":
			evidenceSHA = input.SHA256
		case "human_assessment":
			humanSHA = input.SHA256
		}
	}
	if !reviewSHA256(evidenceSHA) || !reviewSHA256(humanSHA) {
		return "", "", fmt.Errorf("anchor adjudication plan lacks human or evidence authority")
	}
	return evidenceSHA, humanSHA, nil
}

func validateTemporalStructureAnchorAdjudicationConfig(config TemporalStructureAnchorAdjudicationConfig) error {
	paths := []string{config.PublicManifestPath, config.PrivateAuthorityPath, config.PlanAuthoringPath, config.PlanReceiptPath, config.ComparisonPath, config.SubmissionPath, config.OutputPath}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("temporal structure anchor adjudication requires every authority, submission, and output path")
		}
	}
	if len(config.AssessmentPaths) < 2 || config.ExpectedCases <= 0 || config.AdjudicatedAt.IsZero() {
		return fmt.Errorf("temporal structure anchor adjudication requires two assessments, an exact case count, and fixed output time")
	}
	return nil
}
