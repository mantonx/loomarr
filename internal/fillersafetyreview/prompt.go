package fillersafetyreview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

const reviewSystemPrompt = "You are an independent broadcast spoken-safety truth reviewer. Listen directly to the complete attached audio and apply only the supplied private rules. Verify or reject the proposed claim and intervals. Do not use outside knowledge, infer from metadata, transcribe, quote, or repeat speech. If degradation prevents a reliable decision, answer unclear. Return only the required schema."

var errReviewUnclear = errors.New("model review evidence is unclear")

type reviewPromptCase struct {
	Policy            fillersafety.Policy                           `json:"privatePolicy"`
	Claim             string                                        `json:"proposedClaim"`
	Locale            string                                        `json:"locale"`
	Slices            []string                                      `json:"slices"`
	PositiveIntervals []fillersafetycorpus.PreparedPositiveInterval `json:"proposedPositiveIntervals,omitempty"`
}

func reviewSchema(policy fillersafety.Policy) map[string]any {
	ruleIDs := make([]any, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		ruleIDs = append(ruleIDs, rule.ID)
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"verdict", "audibility", "matchedRuleIds", "confirmedIntervalIndexes"},
		"properties": map[string]any{
			"verdict":    map[string]any{"type": "string", "enum": []string{"verified", "rejected", "unclear"}},
			"audibility": map[string]any{"type": "string", "enum": []string{"clear", "degraded", "no_speech"}},
			"matchedRuleIds": map[string]any{
				"type": "array", "maxItems": len(ruleIDs), "uniqueItems": true,
				"items": map[string]any{"type": "string", "enum": ruleIDs},
			},
			"confirmedIntervalIndexes": map[string]any{
				"type": "array", "maxItems": 256, "uniqueItems": true,
				"items": map[string]any{"type": "integer", "minimum": 0, "maximum": 4095},
			},
		},
	}
}

func promptSHA256(policy fillersafety.Policy) string {
	raw, err := json.Marshal(struct {
		Version  string         `json:"version"`
		System   string         `json:"system"`
		Template string         `json:"template"`
		Schema   map[string]any `json:"schema"`
		Tokens   int            `json:"tokens"`
	}{
		Version: reviewPromptVersion, System: reviewSystemPrompt,
		Template: "privatePolicy, proposedClaim, locale, slices, proposedPositiveIntervals",
		Schema:   reviewSchema(policy), Tokens: reviewMaxTokens,
	})
	if err != nil {
		return ""
	}
	return hashBytes(raw)
}

func schemaSHA256(policy fillersafety.Policy) string {
	raw, err := json.Marshal(reviewSchema(policy))
	if err != nil {
		return ""
	}
	return hashBytes(raw)
}

func reviewContent(policy fillersafety.Policy, item fillersafetycorpus.ReviewWorklistCase) (string, error) {
	raw, err := json.Marshal(reviewPromptCase{
		Policy: policy, Claim: item.Claim, Locale: item.Locale, Slices: item.Slices,
		PositiveIntervals: item.PositiveIntervals,
	})
	if err != nil {
		return "", fmt.Errorf("encode private model review prompt")
	}
	return string(raw), nil
}

func decodeObservation(raw string) (modelObservation, error) {
	var observation modelObservation
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&observation); err != nil {
		return modelObservation{}, fmt.Errorf("decode model review observation")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return modelObservation{}, fmt.Errorf("decode model review observation")
	}
	return observation, nil
}

func assessmentFromObservation(
	item fillersafetycorpus.ReviewWorklistCase,
	policy fillersafety.Policy,
	observation modelObservation,
) (fillersafetycert.ReviewAssessment, error) {
	if observation.Verdict != "verified" && observation.Verdict != "rejected" && observation.Verdict != "unclear" ||
		observation.Audibility != "clear" && observation.Audibility != "degraded" && observation.Audibility != "no_speech" ||
		!strictStrings(observation.MatchedRuleIDs) || !strictInts(observation.ConfirmedIntervalIndexes) {
		return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review observation is malformed")
	}
	allowed := make(map[string]struct{}, len(policy.Rules))
	for _, rule := range policy.Rules {
		allowed[rule.ID] = struct{}{}
	}
	for _, ruleID := range observation.MatchedRuleIDs {
		if _, ok := allowed[ruleID]; !ok {
			return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review observation names an unknown rule")
		}
	}
	for _, index := range observation.ConfirmedIntervalIndexes {
		if index < 0 || index >= len(item.PositiveIntervals) {
			return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review observation confirms an unknown interval")
		}
	}
	if observation.Verdict == "unclear" || observation.Audibility == "degraded" {
		return fillersafetycert.ReviewAssessment{}, errReviewUnclear
	}
	assessment := fillersafetycert.ReviewAssessment{CaseID: item.CaseID}
	switch item.Claim {
	case fillersafetycorpus.PreparedCohortKindPositiveCandidate:
		complete := confirmsEveryInterval(item.PositiveIntervals, observation)
		if observation.Verdict == "verified" {
			if observation.Audibility != "clear" || !complete {
				return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review positive verification is inconsistent")
			}
			assessment.Decision = fillersafetycert.ReviewDecisionVerified
			for _, interval := range item.PositiveIntervals {
				assessment.PositiveIntervals = append(assessment.PositiveIntervals, fillersafetycert.PositiveInterval{
					RuleID: interval.RuleID, StartMS: interval.StartMS, EndMS: interval.EndMS,
				})
			}
			return assessment, nil
		}
		if complete {
			return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review positive rejection is inconsistent")
		}
		assessment.Decision = fillersafetycert.ReviewDecisionRejected
		return assessment, nil
	case fillersafetycorpus.PreparedCohortKindCleanCandidate:
		if len(observation.ConfirmedIntervalIndexes) != 0 {
			return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review clean observation confirms an interval")
		}
		if observation.Verdict == "verified" {
			if len(observation.MatchedRuleIDs) != 0 ||
				observation.Audibility != "clear" && observation.Audibility != "no_speech" {
				return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review clean verification is inconsistent")
			}
			assessment.Decision = fillersafetycert.ReviewDecisionVerified
			return assessment, nil
		}
		if observation.Audibility != "clear" || len(observation.MatchedRuleIDs) == 0 {
			return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review clean rejection is inconsistent")
		}
		assessment.Decision = fillersafetycert.ReviewDecisionRejected
		return assessment, nil
	default:
		return fillersafetycert.ReviewAssessment{}, fmt.Errorf("model review claim is invalid")
	}
}

func confirmsEveryInterval(intervals []fillersafetycorpus.PreparedPositiveInterval, observation modelObservation) bool {
	if len(intervals) == 0 || len(observation.ConfirmedIntervalIndexes) != len(intervals) {
		return false
	}
	matched := make(map[string]struct{}, len(observation.MatchedRuleIDs))
	for _, ruleID := range observation.MatchedRuleIDs {
		matched[ruleID] = struct{}{}
	}
	for index, interval := range intervals {
		if observation.ConfirmedIntervalIndexes[index] != index {
			return false
		}
		if _, ok := matched[interval.RuleID]; !ok {
			return false
		}
	}
	return true
}

func strictStrings(values []string) bool {
	return values != nil && slices.IsSorted(values) && len(slices.Compact(slices.Clone(values))) == len(values)
}

func strictInts(values []int) bool {
	return values != nil && slices.IsSorted(values) && len(slices.Compact(slices.Clone(values))) == len(values)
}
