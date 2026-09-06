package fillervisualsafety

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

const candidateBlindOpenRouterSystemPrompt = `You are an independent broadcast visual-safety reviewer. Apply only the supplied policy JSON; do not add general taste, age-rating, source-reputation, or editorial rules.

Inspect the complete attached video and every attached contact sheet. The sheets exhaustively show every frame in the supplied deterministic coverage plan, in chronological row-major order. They supplement the video and do not replace complete-video inspection.

Set coverageAssessment to insufficient unless you processed the complete video and all contact sheets, or if visibility prevents a reliable high-recall screen. For each policy match, return the narrowest conservative source-relative interval. Use observed when the prohibited signal is visibly present and uncertain when visibility is borderline but cannot safely be cleared. Do not identify the source, people, brand, or program. Return no description and no prose outside the required schema. An empty matches array means only that you observed no supplied-policy signal; it is not a certification or permission to air.`

type candidateBlindOpenRouterWire struct {
	CoverageAssessment string                          `json:"coverageAssessment"`
	Matches            []CandidateBlindOpenRouterMatch `json:"matches"`
}

func candidateBlindOpenRouterPromptSHA256() string {
	digest := sha256.Sum256([]byte(CandidateBlindOpenRouterPromptVersion + "\x00" + candidateBlindOpenRouterSystemPrompt))
	return hex.EncodeToString(digest[:])
}

func candidateBlindOpenRouterSchema(policy CandidateBlindReviewPolicy, durationMS int64) map[string]any {
	ids := make([]string, 0, len(policy.PolicyMatches))
	for _, match := range policy.PolicyMatches {
		ids = append(ids, match.ID)
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"coverageAssessment", "matches"},
		"properties": map[string]any{
			"coverageAssessment": map[string]any{"type": "string", "enum": []string{CandidateBlindCoverageCompleted, CandidateBlindCoverageInsufficient}},
			"matches": map[string]any{
				"type": "array", "maxItems": 32,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"policyMatchId", "startMs", "endMs", "certainty"},
					"properties": map[string]any{
						"policyMatchId": map[string]any{"type": "string", "enum": ids},
						"startMs":       map[string]any{"type": "integer", "minimum": 0, "maximum": durationMS - 1},
						"endMs":         map[string]any{"type": "integer", "minimum": 1, "maximum": durationMS},
						"certainty":     map[string]any{"type": "string", "enum": []string{CandidateBlindCertaintyObserved, CandidateBlindCertaintyUncertain}},
					},
				},
			},
		},
	}
}

func candidateBlindOpenRouterSchemaSHA256(policy CandidateBlindReviewPolicy, durationMS int64) string {
	return digestJSON(candidateBlindOpenRouterSchema(policy, durationMS))
}

func candidateBlindOpenRouterContent(policyRaw []byte, manifest CandidateBlindReviewManifest, input CandidateBlindHostedInput) string {
	var builder strings.Builder
	builder.WriteString("Exact policy JSON follows.\n")
	builder.Write(policyRaw)
	if len(policyRaw) == 0 || policyRaw[len(policyRaw)-1] != '\n' {
		builder.WriteByte('\n')
	}
	builder.WriteString("Source duration milliseconds: ")
	builder.WriteString(strconv.FormatInt(manifest.Plan.DurationMS, 10))
	builder.WriteString("\nThe complete video is attached after the contact sheets. Contact sheets are attached in this order:\n")
	for index, sheet := range input.ContactSheets {
		builder.WriteString("sheet ")
		builder.WriteString(strconv.Itoa(index))
		builder.WriteString(": ordinals ")
		builder.WriteString(strconv.Itoa(sheet.FirstOrdinal))
		builder.WriteByte('-')
		builder.WriteString(strconv.Itoa(sheet.LastOrdinal))
		builder.WriteString(", observed milliseconds ")
		for ordinal := sheet.FirstOrdinal; ordinal <= sheet.LastOrdinal; ordinal++ {
			if ordinal > sheet.FirstOrdinal {
				builder.WriteByte(',')
			}
			builder.WriteString(strconv.FormatInt(manifest.Frames[ordinal].ObservedMS, 10))
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func decodeCandidateBlindOpenRouterAssessment(raw string, policy CandidateBlindReviewPolicy, durationMS int64) (CandidateBlindOpenRouterAssessment, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var wire candidateBlindOpenRouterWire
	if err := decoder.Decode(&wire); err != nil {
		return CandidateBlindOpenRouterAssessment{}, errors.New("candidate-blind OpenRouter review response is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CandidateBlindOpenRouterAssessment{}, errors.New("candidate-blind OpenRouter review response has trailing content")
	}
	if wire.CoverageAssessment != CandidateBlindCoverageCompleted && wire.CoverageAssessment != CandidateBlindCoverageInsufficient || len(wire.Matches) > 32 {
		return CandidateBlindOpenRouterAssessment{}, errors.New("candidate-blind OpenRouter review response has invalid coverage")
	}
	allowed := make(map[string]struct{}, len(policy.PolicyMatches))
	for _, match := range policy.PolicyMatches {
		allowed[match.ID] = struct{}{}
	}
	for index, match := range wire.Matches {
		_, known := allowed[match.PolicyMatchID]
		if !known || match.StartMS < 0 || match.StartMS >= match.EndMS || match.EndMS > durationMS ||
			(match.Certainty != CandidateBlindCertaintyObserved && match.Certainty != CandidateBlindCertaintyUncertain) {
			return CandidateBlindOpenRouterAssessment{}, fmt.Errorf("candidate-blind OpenRouter review match %d is invalid", index)
		}
		if index > 0 && compareCandidateBlindMatches(wire.Matches[index-1], match) >= 0 {
			return CandidateBlindOpenRouterAssessment{}, errors.New("candidate-blind OpenRouter review matches are not canonical")
		}
	}
	outcome := CandidateBlindOutcomeNoSignal
	if len(wire.Matches) > 0 {
		outcome = CandidateBlindOutcomeProhibitedSignal
	} else if wire.CoverageAssessment == CandidateBlindCoverageInsufficient {
		outcome = CandidateBlindOutcomeCoverageHold
	}
	return CandidateBlindOpenRouterAssessment{
		CoverageAssessment: wire.CoverageAssessment,
		Matches:            slices.Clone(wire.Matches),
		Outcome:            outcome,
	}, nil
}

func compareCandidateBlindMatches(left, right CandidateBlindOpenRouterMatch) int {
	if left.StartMS != right.StartMS {
		return cmp.Compare(left.StartMS, right.StartMS)
	}
	if left.EndMS != right.EndMS {
		return cmp.Compare(left.EndMS, right.EndMS)
	}
	if value := strings.Compare(left.PolicyMatchID, right.PolicyMatchID); value != 0 {
		return value
	}
	return strings.Compare(left.Certainty, right.Certainty)
}
