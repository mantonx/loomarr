package fillersafety

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouterVideoCorroboratorSendsCompleteSourceAndReturnsNoSignal(t *testing.T) {
	t.Parallel()
	var requestBody []byte
	server := audioResponseServer(t, `{"visualAssessment":"completed","spokenLanguageAssessment":"completed","flags":[]}`, &requestBody)
	defer server.Close()
	reservedAuthority, reservedRequest := "", ""
	config := validOpenRouterVideoConfig(server)
	config.Reserve = func(authoritySHA256, requestSHA256 string) error {
		reservedAuthority, reservedRequest = authoritySHA256, requestSHA256
		return nil
	}
	plan := proposalTestPlan(t)
	attempt, err := (&openRouterVideoCorroborator{config: config}).corroborate(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.State != VideoNoSignal || len(attempt.Flags) != 0 || attempt.Flags == nil || reservedAuthority != plan.AuthoritySHA256 || reservedRequest == "" || reservedRequest != attempt.Transport.RequestSHA256 || !attempt.Transport.ChargeKnown {
		t.Fatalf("attempt=%+v reservation=%q/%q", attempt, reservedAuthority, reservedRequest)
	}
	if !strings.Contains(string(requestBody), `"type":"video_url"`) || !strings.Contains(string(requestBody), `"data:video/mp4;base64,`) || !strings.Contains(string(requestBody), `Duration milliseconds: 30000`) {
		t.Fatal("request omitted complete video or duration authority")
	}
}

func TestOpenRouterVideoCorroboratorRetainsUnprojectablePresenceAsHold(t *testing.T) {
	t.Parallel()
	server := audioResponseServer(t, `{"visualAssessment":"completed","spokenLanguageAssessment":"completed","flags":[{"kind":"explicit_nudity","startMs":900,"endMs":800,"modality":"video"}]}`, nil)
	defer server.Close()
	config := validOpenRouterVideoConfig(server)
	attempt, err := (&openRouterVideoCorroborator{config: config}).corroborate(t.Context(), proposalTestPlan(t))
	if err == nil || attempt.State != VideoProhibitedUnprojectable || len(attempt.Flags) != 1 || attempt.Transport.ResponseSHA256 == "" {
		t.Fatalf("attempt=%+v err=%v", attempt, err)
	}
	public, marshalErr := json.Marshal(attempt.Flags)
	if marshalErr != nil || strings.Contains(string(public), "source.mp4") {
		t.Fatalf("flags leaked source identity: %s err=%v", public, marshalErr)
	}
}

func TestValidateVideoModelOutputPreservesPresenceAndCoverageSemantics(t *testing.T) {
	t.Parallel()
	validNudity := videoFlag{Kind: "explicit_nudity", StartMS: 100, EndMS: 200, Modality: "video"}
	invalidTiming := videoFlag{Kind: "hateful_or_degrading_slur", StartMS: 300, EndMS: 200, Modality: "audio"}
	tests := []struct {
		name   string
		output videoModelOutput
		state  VideoState
		valid  bool
	}{
		{name: "complete no signal", output: videoModelOutput{VisualAssessment: "completed", SpokenAssessment: "completed", Flags: []videoFlag{}}, state: VideoNoSignal, valid: true},
		{name: "valid presence", output: videoModelOutput{VisualAssessment: "completed", SpokenAssessment: "completed", Flags: []videoFlag{validNudity}}, state: VideoProhibited, valid: true},
		{name: "presence outranks incomplete coverage", output: videoModelOutput{VisualAssessment: "insufficient", SpokenAssessment: "insufficient", Flags: []videoFlag{validNudity}}, state: VideoProhibited, valid: true},
		{name: "valid presence outranks malformed companion", output: videoModelOutput{VisualAssessment: "completed", SpokenAssessment: "completed", Flags: []videoFlag{validNudity, invalidTiming}}, state: VideoProhibited, valid: true},
		{name: "unprojectable presence", output: videoModelOutput{VisualAssessment: "completed", SpokenAssessment: "completed", Flags: []videoFlag{invalidTiming}}, state: VideoProhibitedUnprojectable},
		{name: "incomplete coverage", output: videoModelOutput{VisualAssessment: "insufficient", SpokenAssessment: "completed", Flags: []videoFlag{}}, state: VideoIncomplete, valid: true},
		{name: "nil flags", output: videoModelOutput{VisualAssessment: "completed", SpokenAssessment: "completed"}, state: VideoInvalidResponse},
		{name: "unknown kind", output: videoModelOutput{VisualAssessment: "completed", SpokenAssessment: "completed", Flags: []videoFlag{{Kind: "future", StartMS: 100, EndMS: 200, Modality: "video"}}}, state: VideoInvalidResponse},
		{name: "nudity audio mismatch", output: videoModelOutput{VisualAssessment: "completed", SpokenAssessment: "completed", Flags: []videoFlag{{Kind: "explicit_nudity", StartMS: 100, EndMS: 200, Modality: "audio"}}}, state: VideoInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, _, err := validateVideoModelOutput(test.output, 1_000)
			if state != test.state || (err == nil) != test.valid {
				t.Fatalf("state=%s err=%v", state, err)
			}
		})
	}
}

func TestOpenRouterVideoCorroboratorRejectsStaleAuthorityBeforeReservation(t *testing.T) {
	t.Parallel()
	server := audioResponseServer(t, `{"visualAssessment":"completed","spokenLanguageAssessment":"completed","flags":[]}`, nil)
	defer server.Close()
	config := validOpenRouterVideoConfig(server)
	config.PromptSHA256 = strings.Repeat("f", 64)
	called := false
	config.Reserve = func(string, string) error { called = true; return nil }
	attempt, err := (&openRouterVideoCorroborator{config: config}).corroborate(t.Context(), proposalTestPlan(t))
	if err == nil || called || attempt.State != VideoFailed {
		t.Fatalf("stale authority reached reservation: attempt=%+v err=%v", attempt, err)
	}
}

func validOpenRouterVideoConfig(server *httptest.Server) openRouterVideoConfig {
	return openRouterVideoConfig{
		Client: server.Client(), BaseURL: server.URL, APIKey: "secret-key",
		Model: "vendor/model", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider",
		CapabilitySHA256: strings.Repeat("b", 64), PromptSHA256: videoPromptSHA256(),
		MaxChargeNanoUSD: 2_000_000, DisableReasoning: true,
		Reserve: func(string, string) error { return nil },
	}
}
