package fillerreview

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	TemporalSuitabilityPromptVersion = "filler-suitability-direct-video-v2"
	temporalSuitabilityMaxTokens     = 4_096
	temporalSuitabilityRequestTitle  = "Loomarr filler suitability screening"
	suitabilityVisualCompleted       = "completed"
	suitabilityVisualInsufficient    = "insufficient"
	suitabilityLanguageCompleted     = "completed"
	suitabilityLanguageInsufficient  = "insufficient"
)

const temporalSuitabilitySystemPrompt = `Screen one identity-blind candidate filler clip for two prohibited broadcast-suitability signals. This is high-recall screening, not a general content summary.

explicit_nudity means filmed or photographic visible genitals, exposed female breasts or nipples, or clearly exposed buttocks. Flag uncertain borderline visibility rather than silently clearing it. Do not flag ordinary swimwear, cleavage, bare male chests, statues, drawings, or non-explicit affection.

hateful_or_degrading_slur means audible or visibly written use of a group-directed slur targeting race, ethnicity, nationality, religion, sex, sexual orientation, gender identity, or disability. Context, quotation, age, or historical provenance does not make the clip airable. Do not repeat, quote, transcribe, or describe the slur itself.

Inspect the complete supplied video, including its audio, and use the supplied transcript only as an additional fallible signal. Set visualAssessment to insufficient if the video was not processed across its full span or visibility prevents a reliable screen. Set spokenLanguageAssessment to insufficient if the audio was unavailable, not processed across its full span, or too unclear to screen; a transcript alone cannot establish complete spoken-language coverage.

Return only the closed flags and approximate locations. Never return a quote, source identity, person identity, or free-form description. If a prohibited signal may be present but its exact time is uncertain, return the narrowest conservative range that contains it. Empty flags means only that this assessor observed no prohibited signal; it never certifies the clip as clean.`

type temporalSuitabilityWire struct {
	VisualAssessment         string                        `json:"visualAssessment"`
	SpokenLanguageAssessment string                        `json:"spokenLanguageAssessment"`
	Flags                    []temporalSuitabilityFlagWire `json:"flags"`
}

type temporalSuitabilityFlagWire struct {
	Kind     string `json:"kind"`
	StartMS  int64  `json:"startMs"`
	EndMS    int64  `json:"endMs"`
	Modality string `json:"modality"`
}

func temporalSuitabilitySchema(durationMS int64) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"visualAssessment", "spokenLanguageAssessment", "flags"},
		"properties": map[string]any{
			"visualAssessment":         map[string]any{"type": "string", "enum": []string{suitabilityVisualCompleted, suitabilityVisualInsufficient}},
			"spokenLanguageAssessment": map[string]any{"type": "string", "enum": []string{suitabilityLanguageCompleted, suitabilityLanguageInsufficient}},
			"flags": map[string]any{
				"type": "array", "maxItems": 16,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"kind", "startMs", "endMs", "modality"},
					"properties": map[string]any{
						"kind":     map[string]any{"type": "string", "enum": []string{string(SuitabilityExplicitNudity), string(SuitabilityHatefulOrDegradingSlur)}},
						"startMs":  map[string]any{"type": "integer", "minimum": 0, "maximum": durationMS - 1},
						"endMs":    map[string]any{"type": "integer", "minimum": 1, "maximum": durationMS},
						"modality": map[string]any{"type": "string", "enum": []string{string(SuitabilityModalityVideo), string(SuitabilityModalityAudio), string(SuitabilityModalityTranscript)}},
					},
				},
			},
		},
	}
}

func temporalSuitabilityContent(item TemporalTruthEvidenceCase) string {
	var builder strings.Builder
	builder.WriteString("Duration milliseconds: ")
	builder.WriteString(strconv.FormatInt(item.DurationMS, 10))
	builder.WriteString("\nFallible transcript segments follow. Absence from this transcript is not evidence of absence from audio.\n")
	for _, segment := range item.TranscriptSegments {
		builder.WriteString("[")
		builder.WriteString(strconv.FormatInt(segment.StartMS, 10))
		builder.WriteString("-")
		builder.WriteString(strconv.FormatInt(segment.EndMS, 10))
		builder.WriteString("] ")
		builder.WriteString(segment.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func temporalSuitabilityPromptSHA256() string {
	// Bind the complete request contract, including the dynamic parts through a
	// sentinel case. Each real request digest separately binds its duration,
	// transcript, and video bytes.
	contract := struct {
		Version      string         `json:"version"`
		System       string         `json:"system"`
		Content      string         `json:"content"`
		SchemaName   string         `json:"schemaName"`
		Schema       map[string]any `json:"schema"`
		MaxTokens    int            `json:"maxTokens"`
		RequestTitle string         `json:"requestTitle"`
	}{
		Version: TemporalSuitabilityPromptVersion, System: temporalSuitabilitySystemPrompt,
		Content:    temporalSuitabilityContent(TemporalTruthEvidenceCase{DurationMS: 1}),
		SchemaName: "filler_suitability", Schema: temporalSuitabilitySchema(1),
		MaxTokens: temporalSuitabilityMaxTokens, RequestTitle: temporalSuitabilityRequestTitle,
	}
	raw, err := json.Marshal(contract)
	if err != nil {
		panic(err)
	}
	return hashBytes(raw)
}
