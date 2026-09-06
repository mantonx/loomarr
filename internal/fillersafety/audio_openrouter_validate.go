package fillersafety

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

func validateOpenRouterAudioInput(adjudicator *openRouterAudioAdjudicator, ctx context.Context, candidate Candidate, wav []byte, reserve func(string) error) error {
	if adjudicator == nil || ctx == nil || ctx.Err() != nil {
		return fmt.Errorf("spoken-safety audio adjudication input is invalid")
	}
	config := adjudicator.config
	if config.Client == nil || strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" || !boundedAuthorityID(config.Model) || !boundedAuthorityID(config.ResolvedModel) || !boundedAuthorityID(config.UpstreamProvider) || !boundedAuthorityID(config.ProviderSlug) || !validSHA256(config.CapabilitySHA256) || config.PolicySHA256 != policySHA256(config.Policy) || config.PromptSHA256 != audioPromptSHA256(config.Policy) || config.MaxChargeNanoUSD <= 0 || reserve == nil {
		return fmt.Errorf("spoken-safety audio adjudication authority is invalid")
	}
	if err := ValidatePolicy(config.Policy); err != nil {
		return fmt.Errorf("spoken-safety audio adjudication policy is invalid")
	}
	if strings.TrimSpace(candidate.ID) == "" || candidate.StartMS < 0 || candidate.EndMS <= candidate.StartMS || len(wav) < 12 || len(wav) > maxCandidateAudioBytes || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return fmt.Errorf("spoken-safety candidate audio is invalid")
	}
	return nil
}

func validateAudioModelOutput(output audioModelOutput, policy Policy) (AudioState, []string, error) {
	if output.MatchedRuleIDs == nil || !slices.IsSorted(output.MatchedRuleIDs) {
		return AudioInvalidResponse, nil, fmt.Errorf("spoken-safety audio response is not canonical")
	}
	classes := make(map[string]string, len(policy.Rules))
	for _, rule := range policy.Rules {
		classes[rule.ID] = rule.Class
	}
	previous := ""
	hasProhibited := false
	for _, ruleID := range output.MatchedRuleIDs {
		class, exists := classes[ruleID]
		if !exists || ruleID == previous {
			return AudioInvalidResponse, nil, fmt.Errorf("spoken-safety audio response names invalid evidence")
		}
		previous = ruleID
		hasProhibited = hasProhibited || class == PolicyClassProhibited
	}
	matched := slices.Clone(output.MatchedRuleIDs)
	switch output.Decision {
	case "detected":
		if len(matched) == 0 || output.Audibility == "no_speech" || output.Audibility != "clear" && output.Audibility != "degraded" {
			return AudioInvalidResponse, nil, fmt.Errorf("spoken-safety detected response is inconsistent")
		}
		if hasProhibited {
			return AudioDetected, matched, nil
		}
		return AudioUnclear, matched, nil
	case "absent":
		if len(matched) != 0 || output.Audibility != "clear" && output.Audibility != "degraded" && output.Audibility != "no_speech" {
			return AudioInvalidResponse, nil, fmt.Errorf("spoken-safety absent response is inconsistent")
		}
		if output.Audibility == "degraded" {
			return AudioUnclear, matched, nil
		}
		return AudioAbsent, matched, nil
	case "unclear":
		if len(matched) != 0 || output.Audibility != "clear" && output.Audibility != "degraded" && output.Audibility != "no_speech" {
			return AudioInvalidResponse, nil, fmt.Errorf("spoken-safety unclear response is inconsistent")
		}
		return AudioUnclear, matched, nil
	default:
		return AudioInvalidResponse, nil, fmt.Errorf("spoken-safety audio response has an invalid decision")
	}
}
