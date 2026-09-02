package fillersafety

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const (
	maxCandidateAudioBytes = 2 << 20
	audioPromptVersion     = "spoken-safety-native-audio-v1"
	audioMaxTokens         = 1280
)

const audioSystemPrompt = "You are a broadcast spoken-safety adjudicator. Listen directly to the attached audio. Apply only the supplied private rules and their match modes. A token_prefix rule is detected when its listed variant begins an audible spoken token, including a longer inflected token. Do not count homophones, merely similar sounds, instrumental music, or wordless vocals. If degradation prevents a reliable distinction, answer unclear. Never transcribe, quote, or repeat any speech. Return only the required schema."

const audioUserPromptTemplate = "Private rule policy: <private-policy-json>. Decide whether this audio triggers any rule."

type openRouterAudioConfig struct {
	Client           *http.Client
	BaseURL          string
	APIKey           string
	Model            string
	ResolvedModel    string
	UpstreamProvider string
	ProviderSlug     string
	CapabilitySHA256 string
	PromptSHA256     string
	Policy           Policy
	PolicySHA256     string
	MaxChargeNanoUSD int64
	DisableReasoning bool
	Reserve          func(candidateID, requestSHA256 string) error
}

type openRouterAudioAdjudicator struct {
	config openRouterAudioConfig
}

type audioAttempt struct {
	Assessment     AudioAssessment
	MatchedRuleIDs []string
	Transport      openroutermedia.Result
}

type audioModelOutput struct {
	Decision       string   `json:"decision"`
	Audibility     string   `json:"audibility"`
	MatchedRuleIDs []string `json:"matchedRuleIds"`
}

func (a *openRouterAudioAdjudicator) adjudicate(ctx context.Context, candidate Candidate, wav []byte) (audioAttempt, error) {
	attempt := audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: AudioFailed}, MatchedRuleIDs: []string{}}
	if err := validateOpenRouterAudioInput(a, ctx, candidate, wav); err != nil {
		return attempt, err
	}
	policyJSON, err := json.Marshal(a.config.Policy)
	if err != nil {
		return attempt, fmt.Errorf("encode private spoken-safety policy")
	}
	ruleIDs := make([]any, 0, len(a.config.Policy.Rules))
	for _, rule := range a.config.Policy.Rules {
		ruleIDs = append(ruleIDs, rule.ID)
	}
	schema := audioOutputSchema(ruleIDs)
	transport, err := openroutermedia.Call(ctx, a.config.Client, a.config.BaseURL, openroutermedia.Config{
		APIKey: a.config.APIKey, Model: a.config.Model, ResolvedModel: a.config.ResolvedModel,
		UpstreamProvider: a.config.UpstreamProvider, ProviderSlug: a.config.ProviderSlug,
		SchemaName: "spoken_safety_adjudication", Schema: schema,
		SystemPrompt: audioSystemPrompt,
		Content:      "Private rule policy: " + string(policyJSON) + ". Decide whether this audio triggers any rule.",
		Audios:       []openroutermedia.Audio{{Format: "wav", Base64: base64.StdEncoding.EncodeToString(wav)}},
		MaxTokens:    audioMaxTokens, MaxChargeNanoUSD: a.config.MaxChargeNanoUSD,
		DisableReasoning: a.config.DisableReasoning,
		Title:            "Loomarr spoken-safety native-audio adjudication",
		Reserve: func(requestSHA256 string) error {
			return a.config.Reserve(candidate.ID, requestSHA256)
		},
	})
	attempt.Transport = transport
	if err != nil {
		return attempt, err
	}
	var output audioModelOutput
	decoder := json.NewDecoder(bytes.NewBufferString(transport.StructuredOutput))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		attempt.Assessment.State = AudioInvalidResponse
		return attempt, fmt.Errorf("decode spoken-safety audio decision")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		attempt.Assessment.State = AudioInvalidResponse
		return attempt, fmt.Errorf("decode spoken-safety audio decision")
	}
	state, matched, err := validateAudioModelOutput(output, a.config.Policy)
	if err != nil {
		attempt.Assessment.State = AudioInvalidResponse
		return attempt, err
	}
	attempt.Assessment.State = state
	attempt.MatchedRuleIDs = matched
	return attempt, nil
}

func audioOutputSchema(ruleIDs []any) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"decision", "audibility", "matchedRuleIds"},
		"properties": map[string]any{
			"decision":   map[string]any{"type": "string", "enum": []string{"detected", "absent", "unclear"}},
			"audibility": map[string]any{"type": "string", "enum": []string{"clear", "degraded", "no_speech"}},
			"matchedRuleIds": map[string]any{
				"type": "array", "maxItems": len(ruleIDs), "uniqueItems": true,
				"items": map[string]any{"type": "string", "enum": ruleIDs},
			},
		},
	}
}

func policySHA256(policy Policy) string {
	raw, err := json.Marshal(policy)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func audioPromptSHA256(policy Policy) string {
	ruleIDs := make([]any, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		ruleIDs = append(ruleIDs, rule.ID)
	}
	raw, err := json.Marshal(struct {
		Version      string         `json:"version"`
		System       string         `json:"system"`
		UserTemplate string         `json:"userTemplate"`
		Schema       map[string]any `json:"schema"`
		MaxTokens    int            `json:"maxTokens"`
	}{Version: audioPromptVersion, System: audioSystemPrompt, UserTemplate: audioUserPromptTemplate, Schema: audioOutputSchema(ruleIDs), MaxTokens: audioMaxTokens})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
