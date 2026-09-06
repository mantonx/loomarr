package fillersafety

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

func validateOpenRouterVideoInput(corroborator *openRouterVideoCorroborator, ctx context.Context, plan *CompleteMediaPlan) error {
	if corroborator == nil || ctx == nil || ctx.Err() != nil || !validProposalPlan(plan) {
		return fmt.Errorf("spoken-safety video corroboration input is invalid")
	}
	config := corroborator.config
	if config.Client == nil || strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" || !boundedAuthorityID(config.Model) || !boundedAuthorityID(config.ResolvedModel) || !boundedAuthorityID(config.UpstreamProvider) || !boundedAuthorityID(config.ProviderSlug) || !validSHA256(config.CapabilitySHA256) || config.PromptSHA256 != videoPromptSHA256() || config.MaxChargeNanoUSD <= 0 || config.Reserve == nil {
		return fmt.Errorf("spoken-safety video corroboration authority is invalid")
	}
	return nil
}

func videoOutputSchema(durationMS int64) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"visualAssessment", "spokenLanguageAssessment", "flags"},
		"properties": map[string]any{
			"visualAssessment":         map[string]any{"type": "string", "enum": []string{"completed", "insufficient"}},
			"spokenLanguageAssessment": map[string]any{"type": "string", "enum": []string{"completed", "insufficient"}},
			"flags": map[string]any{
				"type": "array", "maxItems": 16,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"kind", "startMs", "endMs", "modality"},
					"properties": map[string]any{
						"kind":     map[string]any{"type": "string", "enum": []string{"explicit_nudity", "hateful_or_degrading_slur"}},
						"startMs":  map[string]any{"type": "integer", "minimum": 0, "maximum": durationMS - 1},
						"endMs":    map[string]any{"type": "integer", "minimum": 1, "maximum": durationMS},
						"modality": map[string]any{"type": "string", "enum": []string{"video", "audio"}},
					},
				},
			},
		},
	}
}

func validateVideoModelOutput(output videoModelOutput, durationMS int64) (VideoState, []videoFlag, error) {
	flags := slices.Clone(output.Flags)
	if flags == nil || len(flags) > 16 || output.VisualAssessment != "completed" && output.VisualAssessment != "insufficient" || output.SpokenAssessment != "completed" && output.SpokenAssessment != "insufficient" {
		return VideoInvalidResponse, nil, fmt.Errorf("spoken-safety video response has invalid coverage")
	}
	validPresence := false
	unprojectablePresence := false
	for _, flag := range flags {
		if flag.Kind != "explicit_nudity" && flag.Kind != "hateful_or_degrading_slur" || flag.Modality != "video" && flag.Modality != "audio" || flag.Kind == "explicit_nudity" && flag.Modality != "video" {
			return VideoInvalidResponse, nil, fmt.Errorf("spoken-safety video response names invalid evidence")
		}
		if flag.StartMS < 0 || flag.EndMS <= flag.StartMS || flag.EndMS > durationMS {
			unprojectablePresence = true
			continue
		}
		validPresence = true
	}
	if validPresence {
		return VideoProhibited, flags, nil
	}
	if unprojectablePresence {
		return VideoProhibitedUnprojectable, flags, fmt.Errorf("spoken-safety video presence cannot be projected")
	}
	if output.VisualAssessment != "completed" || output.SpokenAssessment != "completed" {
		return VideoIncomplete, flags, nil
	}
	return VideoNoSignal, flags, nil
}
