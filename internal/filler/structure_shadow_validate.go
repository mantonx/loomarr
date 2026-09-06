package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func ValidateStructureSplitShadowDecision(decision StructureSplitShadowDecision) error {
	if decision.SchemaVersion != StructureSplitShadowSchemaVersion || decision.ContractVersion != StructureSplitShadowContractVersion || decision.ID == "" || decision.ProposalID == "" || decision.ClipHash == "" || strings.TrimSpace(decision.PolicyVersion) != decision.PolicyVersion || decision.PolicyVersion == "" || len(decision.PolicyVersion) > 128 || decision.ObservedAt.IsZero() || decision.ObservedAt != decision.ObservedAt.UTC() {
		return fmt.Errorf("structure split shadow decision identity is invalid")
	}
	if decision.SourceSHA256 != "" && !isContentHash(decision.SourceSHA256) ||
		decision.AssessmentSHA256 != "" && !isContentHash(decision.AssessmentSHA256) ||
		decision.StructureDecisionSHA256 != "" && !isContentHash(decision.StructureDecisionSHA256) ||
		decision.StructureAuthoritySHA256 != "" && !isContentHash(decision.StructureAuthoritySHA256) {
		return fmt.Errorf("structure split shadow decision source or assessment digest is invalid")
	}
	if err := validateStructureSplitShadowOutcome(decision.Legacy); err != nil {
		return fmt.Errorf("structure split shadow legacy outcome: %w", err)
	}
	if err := validateStructureSplitShadowOutcome(decision.Certified); err != nil {
		return fmt.Errorf("structure split shadow certified outcome: %w", err)
	}
	if decision.SHA256 == "" || decision.SHA256 != StructureSplitShadowDecisionSHA256(decision) || decision.ID != "split-shadow-"+decision.SHA256 {
		return fmt.Errorf("structure split shadow decision digest is invalid")
	}
	return nil
}

func validateStructureSplitShadowOutcome(outcome StructureSplitShadowOutcome) error {
	count := 0
	seen := make(map[[2]int64]struct{})
	for _, group := range [][]StructureSplitShadowSpan{outcome.Confirm, outcome.Hold, outcome.Discard} {
		for index, span := range group {
			if span.StartMs < 0 || span.EndMs <= span.StartMs || index > 0 && (span.StartMs < group[index-1].StartMs || span.StartMs == group[index-1].StartMs && span.EndMs < group[index-1].EndMs) {
				return fmt.Errorf("contains an invalid or non-canonical span")
			}
			key := [2]int64{span.StartMs, span.EndMs}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("repeats a span")
			}
			seen[key] = struct{}{}
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("contains no spans")
	}
	return nil
}

func StructureSplitShadowDecisionSHA256(decision StructureSplitShadowDecision) string {
	decision.ID, decision.SHA256 = "", ""
	decision.Legacy.Confirm = slices.Clone(decision.Legacy.Confirm)
	decision.Legacy.Hold = slices.Clone(decision.Legacy.Hold)
	decision.Legacy.Discard = slices.Clone(decision.Legacy.Discard)
	decision.Certified.Confirm = slices.Clone(decision.Certified.Confirm)
	decision.Certified.Hold = slices.Clone(decision.Certified.Hold)
	decision.Certified.Discard = slices.Clone(decision.Certified.Discard)
	raw, err := json.Marshal(decision)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
