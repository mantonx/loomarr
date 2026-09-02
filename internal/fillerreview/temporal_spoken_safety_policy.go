package fillerreview

import (
	"sort"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

func validateTemporalSpokenSafetyPolicy(policy TemporalSpokenSafetyPolicy) error {
	return fillersafety.ValidatePolicy(policy)
}

func validTemporalSpokenSafetyPolicyID(value string) bool {
	return fillersafety.ValidPolicyID(value)
}

func validTemporalSpokenSafetyRuleID(value string) bool {
	return fillersafety.ValidPolicyRuleID(value)
}

func sortedTemporalSpokenSafetyStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
