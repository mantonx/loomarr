package fillerreview

import (
	"fmt"
	"reflect"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func validateTemporalStructureWindowCorpusPlan(
	plan TemporalStructureWindowCorpusPlan,
	authoring TemporalStructureChallengeAuthoring,
	receipt TemporalStructureHoldoutReceipt,
	seed string,
) error {
	if plan.SchemaVersion != TemporalStructureWindowCorpusSchemaVersion || plan.ContractVersion != TemporalStructureWindowCorpusContractVersion ||
		plan.PlannedAt.IsZero() || plan.PlannedAt.Location() != time.UTC || plan.PlannedAt.Before(receipt.PlannedAt) ||
		!reviewSHA256(plan.HoldoutAuthoringSHA256) || plan.HoldoutAuthoringSHA256 != receipt.AuthoringSHA256 ||
		!reviewSHA256(plan.HoldoutReceiptSHA256) || plan.SeedSHA256 != hashBytes([]byte(seed)) ||
		len(plan.Cases) != TemporalStructureWindowCorpusCases || plan.TrainingAllowed || plan.ProductionAllowed ||
		!reviewSHA256(plan.SHA256) || plan.SHA256 != temporalStructureWindowCorpusPlanSHA256(plan) {
		return fmt.Errorf("window corpus plan identity, lineage, or disposition is invalid")
	}
	patterns := make(map[string]int)
	previousID := ""
	for _, item := range plan.Cases {
		if item.ID <= previousID || item.DurationMS <= fillerstructurewindow.PrimarySpanMS ||
			item.DurationMS > fillerstructurewindow.MaximumSourceDurationMS || item.TargetSeamMS != fillerstructurewindow.PrimarySpanMS {
			return fmt.Errorf("window corpus case identity or duration is invalid")
		}
		previousID = item.ID
		patterns[item.Pattern]++
	}
	for _, pattern := range []string{
		TemporalStructureWindowPatternSeamOverlap,
		TemporalStructureWindowPatternSeamPrimaryLeft,
		TemporalStructureWindowPatternSeamPrimaryRight,
		TemporalStructureWindowPatternCrossingSeam,
	} {
		if patterns[pattern] != TemporalStructureWindowCorpusCasesPerPattern {
			return fmt.Errorf("window corpus pattern %q does not have six cases", pattern)
		}
	}
	for _, pattern := range []string{TemporalStructureWindowPatternDurationLowerEdge, TemporalStructureWindowPatternDurationUpperEdge} {
		if patterns[pattern] != TemporalStructureWindowCorpusEdgeCases {
			return fmt.Errorf("window corpus pattern %q does not have two cases", pattern)
		}
	}
	if len(patterns) != 6 {
		return fmt.Errorf("window corpus contains an unknown construction pattern")
	}
	want, err := constructTemporalStructureWindowCorpusPlan(
		authoring, plan.HoldoutAuthoringSHA256, receipt, plan.HoldoutReceiptSHA256, seed, plan.PlannedAt,
	)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(plan, want) {
		return fmt.Errorf("window corpus plan does not reproduce from locked authority")
	}
	return nil
}
