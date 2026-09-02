package fillersafety

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouterAudioAdjudicatorSendsPrivatePolicyAndReturnsOpaquePresence(t *testing.T) {
	t.Parallel()
	secret := "private restricted phrase"
	policy := audioPolicyFixture(secret)
	var requestBody []byte
	server := audioResponseServer(t, `{"decision":"detected","audibility":"clear","matchedRuleIds":["rule-0123456789abcdef01234567"]}`, &requestBody)
	defer server.Close()
	reservedCandidate, reservedRequest := "", ""
	config := validOpenRouterAudioConfig(server, policy)
	config.Reserve = func(candidateID, requestSHA256 string) error {
		reservedCandidate, reservedRequest = candidateID, requestSHA256
		return nil
	}
	adjudicator := &openRouterAudioAdjudicator{config: config}

	attempt, err := adjudicator.adjudicate(t.Context(), Candidate{ID: "candidate-one", StartMS: 100, EndMS: 800}, validCandidateWAV())
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Assessment.State != AudioDetected || attempt.Assessment.CandidateID != "candidate-one" || len(attempt.MatchedRuleIDs) != 1 || attempt.MatchedRuleIDs[0] != policy.Rules[0].ID || reservedCandidate != "candidate-one" || reservedRequest == "" || reservedRequest != attempt.Transport.RequestSHA256 || !attempt.Transport.ChargeKnown {
		t.Fatalf("attempt=%+v reservation=%q/%q", attempt, reservedCandidate, reservedRequest)
	}
	if !strings.Contains(string(requestBody), secret) || !strings.Contains(string(requestBody), `"type":"input_audio"`) || !strings.Contains(string(requestBody), `"format":"wav"`) {
		t.Fatal("request omitted the private policy or native WAV input")
	}
	public, err := json.Marshal(attempt.Assessment)
	if err != nil || strings.Contains(string(public), secret) || strings.Contains(string(public), "Authorization") {
		t.Fatalf("assessment leaked private request values: %s err=%v", public, err)
	}
}

func TestValidateAudioModelOutputUsesAsymmetricPolicySemantics(t *testing.T) {
	t.Parallel()
	policy := audioPolicyFixture("private restricted phrase")
	prohibitedID, ambiguousID := policy.Rules[0].ID, policy.Rules[1].ID
	tests := []struct {
		name   string
		output audioModelOutput
		state  AudioState
		valid  bool
	}{
		{name: "prohibited detected", output: audioModelOutput{Decision: "detected", Audibility: "degraded", MatchedRuleIDs: []string{prohibitedID}}, state: AudioDetected, valid: true},
		{name: "ambiguous detected", output: audioModelOutput{Decision: "detected", Audibility: "clear", MatchedRuleIDs: []string{ambiguousID}}, state: AudioUnclear, valid: true},
		{name: "clear absent", output: audioModelOutput{Decision: "absent", Audibility: "clear", MatchedRuleIDs: []string{}}, state: AudioAbsent, valid: true},
		{name: "no speech absent", output: audioModelOutput{Decision: "absent", Audibility: "no_speech", MatchedRuleIDs: []string{}}, state: AudioAbsent, valid: true},
		{name: "degraded absent", output: audioModelOutput{Decision: "absent", Audibility: "degraded", MatchedRuleIDs: []string{}}, state: AudioUnclear, valid: true},
		{name: "explicit unclear", output: audioModelOutput{Decision: "unclear", Audibility: "clear", MatchedRuleIDs: []string{}}, state: AudioUnclear, valid: true},
		{name: "missing array", output: audioModelOutput{Decision: "absent", Audibility: "clear"}, state: AudioInvalidResponse},
		{name: "unknown id", output: audioModelOutput{Decision: "detected", Audibility: "clear", MatchedRuleIDs: []string{"rule-ffffffffffffffffffffffff"}}, state: AudioInvalidResponse},
		{name: "duplicate id", output: audioModelOutput{Decision: "detected", Audibility: "clear", MatchedRuleIDs: []string{prohibitedID, prohibitedID}}, state: AudioInvalidResponse},
		{name: "unsorted ids", output: audioModelOutput{Decision: "detected", Audibility: "clear", MatchedRuleIDs: []string{ambiguousID, prohibitedID}}, state: AudioInvalidResponse},
		{name: "detected no speech", output: audioModelOutput{Decision: "detected", Audibility: "no_speech", MatchedRuleIDs: []string{prohibitedID}}, state: AudioInvalidResponse},
		{name: "absent with match", output: audioModelOutput{Decision: "absent", Audibility: "clear", MatchedRuleIDs: []string{prohibitedID}}, state: AudioInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, _, err := validateAudioModelOutput(test.output, policy)
			if state != test.state || (err == nil) != test.valid {
				t.Fatalf("state=%s err=%v", state, err)
			}
		})
	}
}

func TestOpenRouterAudioAdjudicatorRejectsMalformedOutputAndAuthorityBeforeUse(t *testing.T) {
	t.Parallel()
	policy := audioPolicyFixture("private restricted phrase")
	server := audioResponseServer(t, `{"decision":"absent","audibility":"clear","matchedRuleIds":[],"extra":true}`, nil)
	defer server.Close()
	config := validOpenRouterAudioConfig(server, policy)
	adjudicator := &openRouterAudioAdjudicator{config: config}
	attempt, err := adjudicator.adjudicate(t.Context(), Candidate{ID: "candidate-one", StartMS: 100, EndMS: 800}, validCandidateWAV())
	if err == nil || attempt.Assessment.State != AudioInvalidResponse || attempt.Transport.ResponseSHA256 == "" {
		t.Fatalf("attempt=%+v err=%v", attempt, err)
	}

	config.PromptSHA256 = strings.Repeat("f", 64)
	called := false
	config.Reserve = func(string, string) error { called = true; return nil }
	attempt, err = (&openRouterAudioAdjudicator{config: config}).adjudicate(t.Context(), Candidate{ID: "candidate-one", StartMS: 100, EndMS: 800}, validCandidateWAV())
	if err == nil || called || attempt.Assessment.State != AudioFailed {
		t.Fatalf("invalid authority reached reservation: attempt=%+v err=%v", attempt, err)
	}
}

func audioPolicyFixture(secret string) Policy {
	policy := validPolicyFixture()
	policy.Rules[0].Variants = []string{secret}
	policy.Rules = append(policy.Rules, PolicyRule{
		ID: "rule-123456789abcdef012345678", Class: PolicyClassAmbiguous,
		MatchMode: PolicyModeTokenPrefix, Variants: []string{"private ambiguous prefix"},
	})
	return policy
}

func validOpenRouterAudioConfig(server *httptest.Server, policy Policy) openRouterAudioConfig {
	return openRouterAudioConfig{
		Client: server.Client(), BaseURL: server.URL, APIKey: "secret-key",
		Model: "vendor/model", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider",
		CapabilitySHA256: strings.Repeat("a", 64), Policy: policy,
		PolicySHA256: policySHA256(policy), PromptSHA256: audioPromptSHA256(policy),
		MaxChargeNanoUSD: 2_000_000, DisableReasoning: true,
		Reserve: func(string, string) error { return nil },
	}
}

func audioResponseServer(t *testing.T, output string, requestBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if requestBody != nil {
			*requestBody = raw
		}
		response := map[string]any{
			"id": "generation", "model": "vendor/model",
			"choices": []any{map[string]any{"message": map[string]any{"content": output, "reasoning": ""}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "cost": 0.001},
			"openrouter_metadata": map[string]any{
				"attempt":   1,
				"attempts":  []any{map[string]any{"provider": "Pinned Provider", "model": "vendor/model-2026", "status": 200}},
				"endpoints": map[string]any{"available": []any{map[string]any{"provider": "Pinned Provider", "model": "vendor/model-2026", "selected": true}}},
			},
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Error(err)
		}
	}))
}

func validCandidateWAV() []byte {
	return []byte("RIFF\x04\x00\x00\x00WAVEdata")
}
