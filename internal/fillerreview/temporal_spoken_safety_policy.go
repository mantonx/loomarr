package fillerreview

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func validateTemporalSpokenSafetyPolicy(policy TemporalSpokenSafetyPolicy) error {
	if policy.SchemaVersion != TemporalSpokenSafetyPolicySchemaVersion || policy.ContractVersion != TemporalSpokenSafetyPolicyContractVersion || !validTemporalSpokenSafetyPolicyID(policy.PolicyID) || policy.GeneratedAt.IsZero() || policy.MaximumInterSegmentGapMS < 0 || policy.MaximumInterSegmentGapMS > 5_000 || len(policy.Rules) == 0 || len(policy.Rules) > 256 {
		return fmt.Errorf("spoken-safety policy identity, timing, or rule count is invalid")
	}
	seenIDs := map[string]struct{}{}
	seenVariants := map[string]struct{}{}
	for ruleIndex, rule := range policy.Rules {
		if !validTemporalSpokenSafetyRuleID(rule.ID) || rule.Class != TemporalSpokenSafetyMatchProhibited && rule.Class != TemporalSpokenSafetyMatchAmbiguous || rule.MatchMode != TemporalSpokenSafetyModeExactWords && rule.MatchMode != TemporalSpokenSafetyModeTokenPrefix || len(rule.Variants) == 0 || len(rule.Variants) > 16 {
			return fmt.Errorf("spoken-safety policy rule %d is invalid", ruleIndex)
		}
		if _, duplicate := seenIDs[rule.ID]; duplicate {
			return fmt.Errorf("spoken-safety policy repeats a rule id")
		}
		seenIDs[rule.ID] = struct{}{}
		for variantIndex, variant := range rule.Variants {
			tokens := temporalSpokenSafetyWords(variant)
			if len(tokens) == 0 || len(tokens) > 12 || len([]rune(variant)) > 128 {
				return fmt.Errorf("spoken-safety policy rule %d variant %d is invalid", ruleIndex, variantIndex)
			}
			keyBytes, _ := json.Marshal(tokens)
			key := string(keyBytes)
			if _, duplicate := seenVariants[key]; duplicate {
				return fmt.Errorf("spoken-safety policy repeats a normalized variant")
			}
			seenVariants[key] = struct{}{}
		}
	}
	return nil
}

func validTemporalSpokenSafetyPolicyID(value string) bool {
	if len(value) < len("policy-a00") || len(value) > 71 || !strings.HasPrefix(value, "policy-") {
		return false
	}
	for _, r := range value[len("policy-"):] {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validTemporalSpokenSafetyRuleID(value string) bool {
	if len(value) != len("rule-")+24 || !strings.HasPrefix(value, "rule-") {
		return false
	}
	_, err := hex.DecodeString(value[len("rule-"):])
	return err == nil
}

func sortedTemporalSpokenSafetyStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
