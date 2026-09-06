package fillerreview

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalStructureOpenRouterPromptVersion = "filler-temporal-structure-direct-video-v4"
	temporalStructureRoleNone                = "none"
	temporalStructureReasonMaximumCharacters = 320
)

const temporalStructureOpenRouterContentFormat = "Complete video duration: %d milliseconds. Inspect the complete supplied video and return the closed temporal-structure assessment."

const temporalStructureOpenRouterSystemPrompt = `Classify the temporal structure of one complete identity-blind video. Judge the boundaries of the supplied file, not whether its topic resembles an advertisement.

unit definitions:
- standalone: exactly one independently bounded, self-contained inserted item with one cohesive communicative purpose. It may contain many shots or scenes when they belong to that one item.
- compilation: two or more separately bounded items joined in the supplied file. A montage or multiple shots inside one cohesive item is not a compilation.
- programme_excerpt: material whose beginning or ending depends on a larger programme, including an ordinary scene, programme opening, sustained performance, credits/title fragment, or an interior cut from a longer programme.
- unusable: corruption or degradation prevents reliable temporal assessment. Age, poor image quality, or recording overlays alone are not enough while the structure remains assessable.
- unclear: the complete video is available but structural evidence remains genuinely insufficient to choose.

Return decisive timestamps in milliseconds. For a compilation, include the observed internal item joins. For a programme excerpt, include evidence at or near the dependent start/end edges. For standalone, include the moments that establish independent framing. Use at most eight sorted unique timestamps.

role applies only when unit is standalone. Choose commercial, promo, bumper, psa, station_id, trailer, interstitial, or unclear. For every non-standalone unit return role=none, an empty roleDecisiveAtMs array, and an empty roleReason.

Do not infer source identity, do not mention filenames or aliases, and do not claim content suitability. Return only the requested JSON.`

type temporalStructureOpenRouterWire struct {
	Unit             string  `json:"unit"`
	UnitDecisiveAtMS []int64 `json:"unitDecisiveAtMs"`
	UnitReason       string  `json:"unitReason"`
	Role             string  `json:"role"`
	RoleDecisiveAtMS []int64 `json:"roleDecisiveAtMs"`
	RoleReason       string  `json:"roleReason"`
}

func temporalStructureOpenRouterSchema(durationMS int64) map[string]any {
	units := []string{
		string(fillereval.UnitStandalone), string(fillereval.UnitCompilation), string(fillereval.UnitProgrammeExcerpt),
		string(fillereval.UnitUnusable), string(fillereval.UnitUnclear),
	}
	roles := []string{
		temporalStructureRoleNone, string(fillereval.TemporalRoleCommercial), string(fillereval.TemporalRolePromo),
		string(fillereval.TemporalRoleBumper), string(fillereval.TemporalRolePSA), string(fillereval.TemporalRoleStationID),
		string(fillereval.TemporalRoleTrailer), string(fillereval.TemporalRoleInterstitial), string(fillereval.TemporalRoleUnclear),
	}
	// Keep the provider-facing grammar to the portable structured-output subset.
	// CoreWeave's Qwen grammar compiler rejects JSON Schema's uniqueItems
	// keyword. Timestamps are set-like, so normalizeTemporalStructureOpenRouterWire
	// sorts and deduplicates them before the closed validator checks the claim.
	times := map[string]any{"type": "array", "maxItems": temporalStructureMaximumDecisiveTimes, "items": map[string]any{"type": "integer", "minimum": 0, "maximum": durationMS}}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"unit", "unitDecisiveAtMs", "unitReason", "role", "roleDecisiveAtMs", "roleReason"},
		"properties": map[string]any{
			"unit":             map[string]any{"type": "string", "enum": units},
			"unitDecisiveAtMs": times,
			"unitReason":       map[string]any{"type": "string", "maxLength": temporalStructureReasonMaximumCharacters},
			"role":             map[string]any{"type": "string", "enum": roles},
			"roleDecisiveAtMs": times,
			"roleReason":       map[string]any{"type": "string", "maxLength": temporalStructureReasonMaximumCharacters},
		},
	}
}

func normalizeTemporalStructureOpenRouterWire(wire *temporalStructureOpenRouterWire) {
	wire.UnitDecisiveAtMS = normalizedTemporalStructureTimes(wire.UnitDecisiveAtMS)
	wire.RoleDecisiveAtMS = normalizedTemporalStructureTimes(wire.RoleDecisiveAtMS)
}

func normalizedTemporalStructureTimes(values []int64) []int64 {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

func temporalStructureOpenRouterContent(durationMS int64) string {
	return fmt.Sprintf(temporalStructureOpenRouterContentFormat, durationMS)
}

func validateTemporalStructureOpenRouterWire(wire temporalStructureOpenRouterWire, durationMS int64) error {
	unit := fillereval.UnitKind(wire.Unit)
	if !validHumanUnit(unit) || strings.TrimSpace(wire.UnitReason) == "" || len(wire.UnitReason) > temporalStructureReasonMaximumCharacters || !validTemporalStructureTimes(wire.UnitDecisiveAtMS, durationMS, unit == fillereval.UnitUnclear) {
		return fmt.Errorf("direct-video structure unit claim is invalid")
	}
	if unit == fillereval.UnitStandalone {
		role := fillereval.TemporalRole(wire.Role)
		if !validHumanRole(role) || strings.TrimSpace(wire.RoleReason) == "" || len(wire.RoleReason) > temporalStructureReasonMaximumCharacters || !validTemporalStructureTimes(wire.RoleDecisiveAtMS, durationMS, role == fillereval.TemporalRoleUnclear) {
			return fmt.Errorf("direct-video standalone role claim is invalid")
		}
	} else if wire.Role != temporalStructureRoleNone || len(wire.RoleDecisiveAtMS) != 0 || wire.RoleReason != "" {
		return fmt.Errorf("direct-video non-standalone claim carries role output")
	}
	if !slices.IsSorted(wire.UnitDecisiveAtMS) || !slices.IsSorted(wire.RoleDecisiveAtMS) {
		return fmt.Errorf("direct-video decisive timestamps are not sorted")
	}
	return nil
}

func temporalStructureAssessmentFromWire(alias string, wire temporalStructureOpenRouterWire, assessedAt time.Time, call fillereval.TemporalInferenceCall) TemporalStructureAssessment {
	normalizeTemporalStructureOpenRouterWire(&wire)
	unitTimes := slices.Clone(wire.UnitDecisiveAtMS)
	assessment := TemporalStructureAssessment{
		Alias:     alias,
		Unit:      &TemporalStructureUnitClaim{Kind: fillereval.UnitKind(wire.Unit), DecisiveAtMS: unitTimes, Reason: strings.TrimSpace(wire.UnitReason)},
		Inference: temporalInferenceFromCalls(assessedAt, []fillereval.TemporalInferenceCall{call}),
	}
	if assessment.Unit.Kind == fillereval.UnitStandalone {
		roleTimes := slices.Clone(wire.RoleDecisiveAtMS)
		assessment.Role = &TemporalStructureRoleClaim{Kind: fillereval.TemporalRole(wire.Role), DecisiveAtMS: roleTimes, Reason: strings.TrimSpace(wire.RoleReason)}
	}
	return assessment
}

func temporalStructureOpenRouterPromptSHA256() string {
	// A sentinel duration makes the dynamic user message and schema generator
	// part of the prompt identity while each request digest binds its real
	// duration and video bytes.
	contract := struct {
		Version      string         `json:"version"`
		System       string         `json:"system"`
		Content      string         `json:"content"`
		SchemaName   string         `json:"schemaName"`
		Schema       map[string]any `json:"schema"`
		MaxTokens    int            `json:"maxTokens"`
		RequestTitle string         `json:"requestTitle"`
	}{
		Version: TemporalStructureOpenRouterPromptVersion, System: temporalStructureOpenRouterSystemPrompt,
		Content: temporalStructureOpenRouterContent(1), SchemaName: temporalStructureOpenRouterSchemaName,
		Schema: temporalStructureOpenRouterSchema(1), MaxTokens: temporalStructureOpenRouterMaxTokens,
		RequestTitle: temporalStructureOpenRouterTitle,
	}
	raw, err := json.Marshal(contract)
	if err != nil {
		panic(err)
	}
	return hashBytes(raw)
}
