package fillerreview

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const TemporalStructureOpenRouterPromptVersion = fillerstructure.DirectVideoPromptVersion

const temporalStructureOpenRouterSystemPrompt = fillerstructure.DirectVideoSystemPrompt

type temporalStructureOpenRouterWire = fillerstructure.DirectVideoResponse
type temporalStructureOpenRouterSegmentWire = fillerstructure.DirectVideoResponseSegment

func temporalStructureOpenRouterSchema(durationMS int64) map[string]any {
	return fillerstructure.DirectVideoSchema(durationMS)
}

func temporalStructureOpenRouterContent(durationMS int64) string {
	return fillerstructure.DirectVideoContent(durationMS)
}

func normalizeTemporalStructureOpenRouterWire(wire *temporalStructureOpenRouterWire) {
	fillerstructure.NormalizeDirectVideoResponse(wire)
	for index := range wire.Segments {
		wire.Segments[index].DecisiveAtMS = slices.Compact(wire.Segments[index].DecisiveAtMS)
	}
}

func validateTemporalStructureOpenRouterWire(wire temporalStructureOpenRouterWire, durationMS int64) error {
	_, err := fillerstructure.AssessDirectVideoResponse(wire, durationMS)
	if err != nil {
		return fmt.Errorf("direct-video segment plan is invalid: %w", err)
	}
	return nil
}

func temporalStructureAssessmentFromWire(alias string, wire temporalStructureOpenRouterWire, assessedAt time.Time, call fillereval.TemporalInferenceCall) TemporalStructureAssessment {
	durationMS := int64(0)
	if len(wire.Segments) > 0 {
		durationMS = wire.Segments[len(wire.Segments)-1].EndMS
	}
	core, _ := fillerstructure.AssessDirectVideoResponse(wire, durationMS)
	assessment := TemporalStructureAssessment{
		Alias: alias, Inference: temporalInferenceFromCalls(assessedAt, []fillereval.TemporalInferenceCall{call}),
		Unit: &TemporalStructureUnitClaim{
			Kind: fillereval.UnitKind(core.Unit.Kind), DecisiveAtMS: core.Unit.DecisiveAtMS, Reason: core.Unit.Reason,
		},
	}
	if core.Role != nil {
		assessment.Role = &TemporalStructureRoleClaim{
			Kind: fillereval.TemporalRole(core.Role.Kind), DecisiveAtMS: core.Role.DecisiveAtMS, Reason: core.Role.Reason,
		}
	}
	for _, segment := range core.Segments {
		assessment.Segments = append(assessment.Segments, TemporalStructureSegmentClaim{
			StartMS: segment.StartMS, EndMS: segment.EndMS, Role: fillereval.TemporalSegmentRole(segment.Role),
			DecisiveAtMS: segment.DecisiveAtMS, Reason: segment.Reason,
		})
	}
	return assessment
}

func temporalStructureOpenRouterPromptSHA256() string {
	// A sentinel duration makes the dynamic user message and schema generator part of the prompt
	// identity while each request digest binds its real duration and video bytes.
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
