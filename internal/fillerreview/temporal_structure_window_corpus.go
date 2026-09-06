package fillerreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	TemporalStructureWindowCorpusSchemaVersion   = 3
	TemporalStructureWindowCorpusContractVersion = "filler-temporal-structure-window-corpus-plan-v3"
	TemporalStructureWindowCorpusCases           = 28
	TemporalStructureWindowCorpusCasesPerPattern = 6
	TemporalStructureWindowCorpusEdgeCases       = 2

	TemporalStructureWindowFirstShortSourceCeilingMS int64 = 120_000
	TemporalStructureWindowFirstLongSourceCeilingMS  int64 = 302_000
	TemporalStructureWindowLowerEdgeDurationMS       int64 = 121_000
	TemporalStructureWindowUpperEdgeDurationMS       int64 = 301_000

	TemporalStructureWindowPatternSeamOverlap       = "seam_overlap"
	TemporalStructureWindowPatternSeamPrimaryLeft   = "seam_primary_left"
	TemporalStructureWindowPatternSeamPrimaryRight  = "seam_primary_right"
	TemporalStructureWindowPatternCrossingSeam      = "crossing_seam"
	TemporalStructureWindowPatternDurationLowerEdge = "duration_lower_edge"
	TemporalStructureWindowPatternDurationUpperEdge = "duration_upper_edge"
)

type TemporalStructureWindowCorpusConfig struct {
	HoldoutAuthoringPath string
	HoldoutReceiptPath   string
	Seed                 string
	PlannedAt            time.Time
	OutputDir            string
}

type TemporalStructureWindowCorpusPlan struct {
	SchemaVersion          int                                 `json:"schemaVersion"`
	ContractVersion        string                              `json:"contractVersion"`
	PlannedAt              time.Time                           `json:"plannedAt"`
	HoldoutAuthoringSHA256 string                              `json:"holdoutAuthoringSha256"`
	HoldoutReceiptSHA256   string                              `json:"holdoutReceiptSha256"`
	SeedSHA256             string                              `json:"seedSha256"`
	Cases                  []TemporalStructureWindowCorpusCase `json:"cases"`
	TrainingAllowed        bool                                `json:"trainingAllowed"`
	ProductionAllowed      bool                                `json:"productionAllowed"`
	SHA256                 string                              `json:"sha256"`
}

type TemporalStructureWindowCorpusCase struct {
	ID               string                              `json:"id"`
	Pattern          string                              `json:"pattern"`
	TargetSeamMS     int64                               `json:"targetSeamMs"`
	TargetBoundaryMS int64                               `json:"targetBoundaryMs,omitempty"`
	DurationMS       int64                               `json:"durationMs"`
	FillerFamilyIDs  []string                            `json:"fillerFamilyIds"`
	Segments         []TemporalStructureChallengeSegment `json:"segments"`
	Truth            []fillerstructure.Segment           `json:"truth"`
}

type TemporalStructureWindowCorpusResult struct {
	Cases          int
	PlanSHA256     string
	PlanFileSHA256 string
}

// BuildTemporalStructureWindowCorpusPlan validates the locked 60-case construction authority and
// publishes one private, deterministic long-reel construction plan. It performs no media work.
func BuildTemporalStructureWindowCorpusPlan(config TemporalStructureWindowCorpusConfig) (TemporalStructureWindowCorpusResult, error) {
	if strings.TrimSpace(config.HoldoutAuthoringPath) == "" || strings.TrimSpace(config.HoldoutReceiptPath) == "" ||
		strings.TrimSpace(config.Seed) == "" || config.PlannedAt.IsZero() || strings.TrimSpace(config.OutputDir) == "" {
		return TemporalStructureWindowCorpusResult{}, fmt.Errorf("window corpus requires holdout authoring, receipt, private seed, fixed planning time, and output")
	}
	authoringRaw, err := os.ReadFile(config.HoldoutAuthoringPath)
	if err != nil {
		return TemporalStructureWindowCorpusResult{}, fmt.Errorf("read window corpus holdout authoring: %w", err)
	}
	receiptRaw, err := os.ReadFile(config.HoldoutReceiptPath)
	if err != nil {
		return TemporalStructureWindowCorpusResult{}, fmt.Errorf("read window corpus holdout receipt: %w", err)
	}
	authoring, err := readStrictJSON[TemporalStructureChallengeAuthoring](config.HoldoutAuthoringPath)
	if err != nil {
		return TemporalStructureWindowCorpusResult{}, fmt.Errorf("decode window corpus holdout authoring: %w", err)
	}
	receipt, err := readStrictJSON[TemporalStructureHoldoutReceipt](config.HoldoutReceiptPath)
	if err != nil {
		return TemporalStructureWindowCorpusResult{}, fmt.Errorf("decode window corpus holdout receipt: %w", err)
	}
	if hashBytes(authoringRaw) != receipt.AuthoringSHA256 {
		return TemporalStructureWindowCorpusResult{}, fmt.Errorf("window corpus receipt does not bind holdout authoring bytes")
	}
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, nil); err != nil {
		return TemporalStructureWindowCorpusResult{}, err
	}
	plan, err := constructTemporalStructureWindowCorpusPlan(
		authoring, hashBytes(authoringRaw), receipt, hashBytes(receiptRaw), config.Seed, config.PlannedAt.UTC(),
	)
	if err != nil {
		return TemporalStructureWindowCorpusResult{}, err
	}
	if err := validateTemporalStructureWindowCorpusPlan(plan, authoring, receipt, config.Seed); err != nil {
		return TemporalStructureWindowCorpusResult{}, err
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return TemporalStructureWindowCorpusResult{}, err
	}
	raw = append(raw, '\n')
	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalStructureWindowCorpusResult{}, err
	}
	defer stage.Cleanup()
	if err := writeTemporalTruthNew(filepath.Join(stage.path, "plan.json"), raw, 0o600); err != nil {
		return TemporalStructureWindowCorpusResult{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalStructureWindowCorpusResult{}, err
	}
	return TemporalStructureWindowCorpusResult{
		Cases: len(plan.Cases), PlanSHA256: plan.SHA256, PlanFileSHA256: hashBytes(raw),
	}, nil
}

func temporalStructureWindowCorpusPlanSHA256(plan TemporalStructureWindowCorpusPlan) string {
	plan.SHA256 = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
