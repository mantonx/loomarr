package fillerreview

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalStructureHoldoutSchemaVersion         = 1
	TemporalStructureHoldoutLegacyContractVersion = "filler-temporal-structure-holdout-plan-v3"
	TemporalStructureHoldoutContractVersion       = "filler-temporal-structure-holdout-plan-v4"
	TemporalStructureHoldoutPlanGenesis           = "genesis"
	TemporalStructureHoldoutPlanReplacement       = "replacement"
	TemporalStructureHoldoutCases                 = 36
	temporalStructureHoldoutClassCases            = 12
	temporalStructureHoldoutParentSources         = 6
)

type TemporalStructureHoldoutConfig struct {
	SelectionPath           string
	EvidenceManifestPath    string
	EvidencePrivateMapPath  string
	HumanAssessmentPath     string
	HumanAttestationPath    string
	MediaQualityPath        string
	SuitabilityPath         string
	ReferenceAuditPath      string
	FamilyAuditPath         string
	TransitionAuthorityPath string
	ProgrammeInventoryPath  string
	SourceRoot              string
	Seed                    string
	Genesis                 bool
	PriorAdjudicationPaths  []string
	PlannedAt               time.Time
	OutputDir               string
}

type TemporalStructureHoldoutProgrammeInventory struct {
	SchemaVersion   int                                `json:"schemaVersion"`
	ContractVersion string                             `json:"contractVersion"`
	GeneratedAt     time.Time                          `json:"generatedAt"`
	Sources         []TemporalStructureChallengeSource `json:"sources"`
}

type TemporalStructureHoldoutReceipt struct {
	SchemaVersion              int                                       `json:"schemaVersion"`
	ContractVersion            string                                    `json:"contractVersion"`
	PlannedAt                  time.Time                                 `json:"plannedAt"`
	PlanKind                   string                                    `json:"planKind,omitempty"`
	SeedSHA256                 string                                    `json:"seedSha256"`
	Inputs                     []TemporalStructureHoldoutInput           `json:"inputs"`
	AuthoringSHA256            string                                    `json:"authoringSha256"`
	Cases                      int                                       `json:"cases"`
	StandaloneCases            int                                       `json:"standaloneCases"`
	CompilationCases           int                                       `json:"compilationCases"`
	ProgrammeExcerptCases      int                                       `json:"programmeExcerptCases"`
	IndependentSources         int                                       `json:"independentSources"`
	ProgrammeParents           int                                       `json:"programmeParents"`
	StandaloneRoleCounts       map[fillereval.TemporalRole]int           `json:"standaloneRoleCounts"`
	SelectedAnchors            []TemporalStructureHoldoutAnchor          `json:"selectedAnchors"`
	CompilationConstructions   []TemporalStructureHoldoutCompilation     `json:"compilationConstructions"`
	ProgrammeConstructions     []TemporalStructureHoldoutProgrammeCut    `json:"programmeConstructions"`
	PriorExposure              TemporalStructureHoldoutTrainingExclusion `json:"priorExposure,omitempty"`
	FutureTrainingExclusion    TemporalStructureHoldoutTrainingExclusion `json:"futureTrainingExclusion"`
	TrainingAllowed            bool                                      `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                                      `json:"productionAdmissionAllowed"`
}

type TemporalStructureHoldoutTrainingExclusion struct {
	Split               string                                        `json:"split"`
	SourceSHA256        []string                                      `json:"sourceSha256"`
	FamilyIDs           []string                                      `json:"familyIds"`
	ProgrammeProvenance []TemporalStructureHoldoutProgrammeProvenance `json:"programmeProvenance"`
}

type TemporalStructureHoldoutProgrammeProvenance struct {
	Authority string `json:"authority"`
	Reference string `json:"reference"`
}

type TemporalStructureHoldoutInput struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type TemporalStructureHoldoutAnchor struct {
	EvidenceAlias string                  `json:"evidenceAlias"`
	CaseID        string                  `json:"caseId"`
	SourceID      string                  `json:"sourceId"`
	FamilyID      string                  `json:"familyId"`
	Role          fillereval.TemporalRole `json:"role"`
	DurationMS    int64                   `json:"durationMs"`
	RankSHA256    string                  `json:"rankSha256"`
}

type TemporalStructureHoldoutCompilation struct {
	CaseID            string                    `json:"caseId"`
	FirstSourceID     string                    `json:"firstSourceId"`
	SecondSourceID    string                    `json:"secondSourceId"`
	JoinBand          string                    `json:"joinBand"`
	TransitionStratum TemporalTransitionStratum `json:"transitionStratum"`
	JoinAtMS          int64                     `json:"joinAtMs"`
	DurationMS        int64                     `json:"durationMs"`
	Roles             []string                  `json:"roles"`
}

type TemporalStructureHoldoutProgrammeCut struct {
	CaseID      string `json:"caseId"`
	SourceID    string `json:"sourceId"`
	Pattern     string `json:"pattern"`
	StartMS     int64  `json:"startMs"`
	DurationMS  int64  `json:"durationMs"`
	ParentEndMS int64  `json:"parentEndMs"`
}

type TemporalStructureHoldoutResult struct {
	Cases           int
	AuthoringSHA256 string
	ReceiptSHA256   string
}

// BuildTemporalStructureHoldoutPlan validates every frozen authority and
// emits only coordinator-private construction authoring plus its receipt. It
// performs no rendering, provider call, training, or admission decision.
func BuildTemporalStructureHoldoutPlan(config TemporalStructureHoldoutConfig) (TemporalStructureHoldoutResult, error) {
	if err := validateTemporalStructureHoldoutConfig(config); err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	defer stage.Cleanup()
	loaded, err := loadTemporalStructureHoldout(config)
	if err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	anchors, err := selectTemporalStructureHoldoutAnchors(config.Seed, loaded)
	if err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	parents, err := selectTemporalStructureHoldoutParents(config.Seed, loaded.programmeInventory, loaded.prior.exposure)
	if err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	authoring, receipt, err := constructTemporalStructureHoldout(config, loaded, anchors, parents)
	if err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	authoringRaw, err := json.MarshalIndent(authoring, "", "  ")
	if err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	authoringRaw = append(authoringRaw, '\n')
	receipt.AuthoringSHA256 = hashBytes(authoringRaw)
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, &loaded.transition); err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	receiptRaw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	receiptRaw = append(receiptRaw, '\n')
	if err := writeTemporalTruthNew(filepath.Join(stage.path, "authoring.json"), authoringRaw, 0o600); err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(stage.path, "receipt.json"), receiptRaw, 0o600); err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalStructureHoldoutResult{}, err
	}
	return TemporalStructureHoldoutResult{Cases: len(authoring.Cases), AuthoringSHA256: receipt.AuthoringSHA256, ReceiptSHA256: hashBytes(receiptRaw)}, nil
}

func validateTemporalStructureHoldoutConfig(config TemporalStructureHoldoutConfig) error {
	paths := []string{
		config.SelectionPath, config.EvidenceManifestPath, config.EvidencePrivateMapPath,
		config.HumanAssessmentPath, config.HumanAttestationPath, config.MediaQualityPath,
		config.SuitabilityPath, config.ReferenceAuditPath, config.FamilyAuditPath, config.TransitionAuthorityPath, config.ProgrammeInventoryPath,
		config.SourceRoot, config.OutputDir,
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("temporal structure holdout requires every authority path, source root, and output")
		}
	}
	validLineage := config.Genesis && len(config.PriorAdjudicationPaths) == 0 || !config.Genesis && len(config.PriorAdjudicationPaths) > 0
	if strings.TrimSpace(config.Seed) == "" || config.PlannedAt.IsZero() || !validLineage {
		return fmt.Errorf("temporal structure holdout requires a private seed, fixed planning time, and exactly one genesis or prior-adjudication lineage mode")
	}
	return nil
}
