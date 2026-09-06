package fillersafety

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/openroutermedia"
	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

func TestOpenRouterAudioAdjudicatorSendsPrivatePolicyAndReturnsOpaquePresence(t *testing.T) {
	t.Parallel()
	secret := "private restricted phrase"
	policy := audioPolicyFixture(secret)
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: openRouterResponse(t, `{"decision":"detected","audibility":"clear","matchedRuleIds":["rule-0123456789abcdef01234567"]}`)})
	reservedCandidate, reservedRequest := "", ""
	config := validOpenRouterAudioConfig(&http.Client{Transport: transport}, policy)
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
	requests := transport.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests=%+v", requests)
	}
	if !strings.Contains(string(requests[0].Body), secret) || !strings.Contains(string(requests[0].Body), `"type":"input_audio"`) || !strings.Contains(string(requests[0].Body), `"format":"wav"`) {
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
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: openRouterResponse(t, `{"decision":"absent","audibility":"clear","matchedRuleIds":[],"extra":true}`)})
	config := validOpenRouterAudioConfig(&http.Client{Transport: transport}, policy)
	adjudicator := &openRouterAudioAdjudicator{config: config}
	attempt, err := adjudicator.adjudicate(t.Context(), Candidate{ID: "candidate-one", StartMS: 100, EndMS: 800}, validCandidateWAV())
	if err == nil || attempt.Assessment.State != AudioInvalidResponse || attempt.Transport.ResponseSHA256 == "" {
		t.Fatalf("attempt=%+v err=%v", attempt, err)
	}

	config.CapabilitySHA256 = strings.Repeat("f", 64)
	called := false
	config.Reserve = func(string, string) error { called = true; return nil }
	attempt, err = (&openRouterAudioAdjudicator{config: config}).adjudicate(t.Context(), Candidate{ID: "candidate-one", StartMS: 100, EndMS: 800}, validCandidateWAV())
	if err == nil || called || attempt.Assessment.State != AudioFailed {
		t.Fatalf("invalid authority reached reservation: attempt=%+v err=%v", attempt, err)
	}
}

func TestOpenRouterAudioAdjudicatorRejectsMissingAudioCapabilityBeforeUse(t *testing.T) {
	t.Parallel()
	policy := audioPolicyFixture("private restricted phrase")
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: openRouterResponse(t, `{"decision":"absent","audibility":"clear","matchedRuleIds":[]}`)})
	config := validOpenRouterAudioConfig(&http.Client{Transport: transport}, policy)
	config.Snapshot.Models[0].InputModalities = []string{"text", "video"}
	config.CapabilitySHA256 = openroutermedia.CapabilitySnapshotSHA256(config.Snapshot)
	reserved := false
	config.Reserve = func(string, string) error { reserved = true; return nil }
	attempt, err := (&openRouterAudioAdjudicator{config: config}).adjudicate(t.Context(), Candidate{ID: "candidate-one", StartMS: 100, EndMS: 800}, validCandidateWAV())
	if err == nil || reserved || len(transport.Requests()) != 0 || attempt.Assessment.State != AudioFailed {
		t.Fatalf("attempt=%+v err=%v reserved=%t requests=%d", attempt, err, reserved, len(transport.Requests()))
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

func validOpenRouterAudioConfig(client *http.Client, policy Policy) openRouterAudioConfig {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	snapshot := validOpenRouterSafetySnapshot(now)
	return openRouterAudioConfig{
		Client: client, Snapshot: snapshot, Now: func() time.Time { return now }, BaseURL: "https://openrouter.test/api/v1", APIKey: "secret-key",
		Model: "vendor/model", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider",
		CapabilitySHA256: openroutermedia.CapabilitySnapshotSHA256(snapshot), Policy: policy,
		PolicySHA256: policySHA256(policy), PromptSHA256: audioPromptSHA256(policy),
		MaxChargeNanoUSD: 2_000_000, DisableReasoning: true,
		Reserve: func(string, string) error { return nil },
	}
}

func validOpenRouterSafetySnapshot(now time.Time) openroutermedia.CapabilitySnapshot {
	return openroutermedia.CapabilitySnapshot{
		SchemaVersion: openroutermedia.CapabilitySnapshotSchemaVersion, SourceBaseURL: "https://openrouter.test/api/v1", RetrievedAt: now,
		Requests: 3, ResponseBytes: 1, Models: []openroutermedia.CapabilityModelSnapshot{{
			ID: "vendor/model", CanonicalSlug: "vendor/model-2026", Name: "Model", Created: 1,
			InputModalities: []string{"audio", "text", "video"}, OutputModalities: []string{"text"},
			Endpoints: []openroutermedia.CapabilityEndpointSnapshot{{Name: "Pinned", ModelID: "vendor/model", ProviderName: "Pinned Provider", ProviderSlug: "pinned/provider", ContextLength: 4096, MaxCompletionTokens: 4096, SupportedParameters: []string{"response_format", "structured_outputs"}, Pricing: map[string]string{"completion": "0.000001", "prompt": "0.000001"}, ZDR: true}},
		}},
	}
}

func openRouterResponse(t *testing.T, output string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id": "generation", "model": "vendor/model",
		"choices": []any{map[string]any{"message": map[string]any{"content": output, "reasoning": ""}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "cost": 0.001},
		"openrouter_metadata": map[string]any{
			"attempt":   1,
			"attempts":  []any{map[string]any{"provider": "Pinned Provider", "model": "vendor/model-2026", "status": 200}},
			"endpoints": map[string]any{"available": []any{map[string]any{"provider": "Pinned Provider", "model": "vendor/model-2026", "selected": true}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}
}

func validCandidateWAV() []byte {
	return []byte("RIFF\x04\x00\x00\x00WAVEdata")
}
