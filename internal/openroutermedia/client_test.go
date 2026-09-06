package openroutermedia

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

func TestCallReservesExactRequestBeforeHTTP(t *testing.T) {
	t.Parallel()
	var reservedHash string
	var requestWire structuredRequest
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: response(http.StatusOK, validResponse())})
	client := &http.Client{Transport: transport}

	config := validConfig(func(requestSHA256 string) error {
		reservedHash = requestSHA256
		return nil
	})
	config.Images = []string{"aW1hZ2U="}
	config.Audios = []Audio{{Format: "wav", Base64: "YXVkaW8="}}
	config.Videos = []Video{{MIMEType: "video/mp4", Base64: "dmlkZW8="}}
	config.DisableReasoning = true
	result, err := Call(t.Context(), client, "https://openrouter.test/api/v1", config)
	if err != nil {
		t.Fatal(err)
	}
	requests := transport.Requests()
	if len(requests) != 1 || requests[0].Method != http.MethodPost || requests[0].URL != "https://openrouter.test/api/v1/chat/completions" {
		t.Fatalf("requests=%+v", requests)
	}
	request := requests[0]
	if reservedHash == "" || reservedHash != hashBytes(request.Body) {
		t.Errorf("request was not durably reserved before HTTP: reserved=%q actual=%q", reservedHash, hashBytes(request.Body))
	}
	if err := json.Unmarshal(request.Body, &requestWire); err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("X-OpenRouter-Metadata") != "enabled" || request.Header.Get("X-OpenRouter-Title") != "contract test" {
		t.Errorf("request headers=%v", request.Header)
	}
	parts := requestWire.Messages[1].Content
	if result.RequestSHA256 != reservedHash || result.ResponseSHA256 != hashBytes(result.RawResponse) || !result.ChargeKnown || result.ChargedNanoUSD != 1_000_000 || result.ChargedAmountUSD != "0.001" || result.StructuredOutput != `{"ok":true}` || result.GenerationID != "generation" || result.PromptTokens != 10 || result.CompletionTokens != 2 {
		t.Fatalf("result=%+v", result)
	}
	if len(parts) != 4 || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/jpeg;base64,aW1hZ2U=" || parts[2].Audio == nil || parts[2].Audio.Format != "wav" || parts[2].Audio.Data != "YXVkaW8=" || parts[3].VideoURL == nil || parts[3].VideoURL.URL != "data:video/mp4;base64,dmlkZW8=" || requestWire.Provider.AllowFallbacks || !requestWire.Provider.RequireParameters || !requestWire.Provider.ZDR || requestWire.Provider.DataCollection != "deny" || requestWire.Reasoning == nil || requestWire.Reasoning.Enabled || !requestWire.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("request=%+v", requestWire)
	}
}

func TestCallRejectsInvalidAudioBeforeReservation(t *testing.T) {
	t.Parallel()
	reserved := false
	_, err := Call(t.Context(), http.DefaultClient, "https://openrouter.ai/api/v1", Config{
		Audios:  []Audio{{Format: "exe", Base64: "YWJj"}},
		Reserve: func(string) error { reserved = true; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid format") || reserved {
		t.Fatalf("err=%v reserved=%t", err, reserved)
	}
}

func TestCallRequiresReservationBeforeHTTP(t *testing.T) {
	t.Parallel()
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}
	result, err := Call(t.Context(), client, "https://openrouter.ai/api/v1", Config{})
	if err == nil || !strings.Contains(err.Error(), "durable reservation") || called || result.RequestSHA256 == "" {
		t.Fatalf("result=%+v err=%v called=%t", result, err, called)
	}
}

func TestCallRejectsInvalidVideoBeforeReservation(t *testing.T) {
	t.Parallel()
	reserved := false
	_, err := Call(t.Context(), http.DefaultClient, "https://openrouter.ai/api/v1", Config{
		Videos:  []Video{{MIMEType: "application/octet-stream", Base64: "YWJj"}},
		Reserve: func(string) error { reserved = true; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid MIME") || reserved {
		t.Fatalf("err=%v reserved=%t", err, reserved)
	}
}

func TestCallRetainsNonOKResponseAuthority(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"error":"temporarily unavailable"}`)
	client := &http.Client{Transport: httpfixture.NewScriptedTransport(httpfixture.Step{Response: response(http.StatusServiceUnavailable, raw)})}
	result, err := Call(t.Context(), client, "https://openrouter.test/api/v1", validConfig(func(string) error { return nil }))
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable || result.ResponseSHA256 != hashBytes(raw) || !bytes.Equal(result.RawResponse, raw) || result.ChargeKnown {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCallReturnsNestedChoiceErrorAsSettledProviderStatus(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: httpfixture.NewScriptedTransport(httpfixture.Step{Response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"id":"generation","model":"vendor/model","choices":[{"error":{"code":429,"message":"temporarily rate-limited upstream"},"message":{"content":null,"reasoning":"partial"}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"cost":0}}`)),
		Header:     make(http.Header),
	}})}
	result, err := Call(t.Context(), client, "https://openrouter.test/api/v1", validConfig(func(string) error { return nil }))
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests || !result.ChargeKnown || result.ChargedNanoUSD != 0 || result.ResponseSHA256 == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCallFailsClosedAfterSettlement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing cost", body: `{"id":"generation","model":"vendor/model","choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1},"openrouter_metadata":{"attempt":1,"endpoints":{"available":[{"provider":"Pinned Provider","model":"vendor/model-2026","selected":true}]}}}`, want: "missing or out-of-reservation cost"},
		{name: "over ceiling", body: `{"id":"generation","model":"vendor/model","choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"cost":1},"openrouter_metadata":{"attempt":1,"endpoints":{"available":[{"provider":"Pinned Provider","model":"vendor/model-2026","selected":true}]}}}`, want: "missing or out-of-reservation cost"},
		{name: "requested model drift", body: `{"id":"generation","model":"other/model","choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"cost":0.001},"openrouter_metadata":{"attempt":1,"endpoints":{"available":[{"provider":"Pinned Provider","model":"vendor/model-2026","selected":true}]}}}`, want: "does not bind"},
		{name: "selected route drift", body: `{"id":"generation","model":"vendor/model","choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"cost":0.001},"openrouter_metadata":{"attempt":1,"endpoints":{"available":[{"provider":"Other Provider","model":"vendor/model-2026","selected":true}]}}}`, want: "does not bind"},
		{name: "multiple attempts", body: `{"id":"generation","model":"vendor/model","choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"cost":0.001},"openrouter_metadata":{"attempt":1,"attempts":[{"provider":"Pinned Provider","model":"vendor/model-2026","status":200},{"provider":"Pinned Provider","model":"vendor/model-2026","status":200}],"endpoints":{"available":[{"provider":"Pinned Provider","model":"vendor/model-2026","selected":true}]}}}`, want: "does not bind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: httpfixture.NewScriptedTransport(httpfixture.Step{Response: response(http.StatusOK, []byte(test.body))})}
			result, err := Call(t.Context(), client, "https://openrouter.test/api/v1", validConfig(func(string) error { return nil }))
			if err == nil || !strings.Contains(err.Error(), test.want) || result.ResponseSHA256 == "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCallRejectsOversizedResponseAfterReservation(t *testing.T) {
	t.Parallel()
	reserved := false
	client := &http.Client{Transport: httpfixture.NewScriptedTransport(httpfixture.Step{Response: response(http.StatusOK, bytes.Repeat([]byte("x"), maxResponseBytes+1))})}
	result, err := Call(t.Context(), client, "https://openrouter.test/api/v1", validConfig(func(string) error {
		reserved = true
		return nil
	}))
	if err == nil || !strings.Contains(err.Error(), "byte ceiling") || !reserved || result.RequestSHA256 == "" || result.ResponseSHA256 != "" || len(result.RawResponse) != 0 {
		t.Fatalf("result=%+v err=%v reserved=%t", result, err, reserved)
	}
}

func TestCallDoesNotExposeProviderControlledErrorDetail(t *testing.T) {
	t.Parallel()
	const bodySecret = "provider-body-private-input"
	const choiceSecret = "choice-message-private-input"
	const modelSecret = "provider-model-private-route"
	const metadataSecret = "metadata-private-route"
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantStatus int
		rawSecrets []string
	}{
		{name: "non-ok body", statusCode: http.StatusServiceUnavailable, body: `{"error":"` + bodySecret + `"}`, wantStatus: http.StatusServiceUnavailable, rawSecrets: []string{bodySecret}},
		{name: "top-level error", statusCode: http.StatusOK, body: `{"error":{"message":"` + bodySecret + `"}}`, rawSecrets: []string{bodySecret}},
		{name: "nested choice and metadata", statusCode: http.StatusOK, body: `{"id":"generation","model":"vendor/model","choices":[{"error":{"code":429,"message":"` + choiceSecret + `"},"message":{"content":"","reasoning":""}}],"usage":{"cost":0},"openrouter_metadata":{"attempt":1,"attempts":[{"provider":"` + metadataSecret + `","model":"vendor/model-2026","status":200}],"endpoints":{"available":[{"provider":"` + metadataSecret + `","model":"vendor/model-2026","selected":true}]}}}`, wantStatus: http.StatusTooManyRequests, rawSecrets: []string{choiceSecret, metadataSecret}},
		{name: "model and metadata binding mismatch", statusCode: http.StatusOK, body: `{"id":"generation","model":"` + modelSecret + `","choices":[{"message":{"content":"{}","reasoning":""}}],"usage":{"cost":0},"openrouter_metadata":{"attempt":1,"attempts":[{"provider":"` + metadataSecret + `","model":"` + modelSecret + `","status":200}],"endpoints":{"available":[{"provider":"` + metadataSecret + `","model":"` + modelSecret + `","selected":true}]}}}`, rawSecrets: []string{modelSecret, metadataSecret}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: httpfixture.NewScriptedTransport(httpfixture.Step{Response: response(test.statusCode, []byte(test.body))})}
			result, err := Call(t.Context(), client, "https://openrouter.test/api/v1", validConfig(func(string) error { return nil }))
			if err == nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			for _, secret := range test.rawSecrets {
				if !bytes.Contains(result.RawResponse, []byte(secret)) {
					t.Fatalf("private evidence did not retain %q: result=%+v", secret, result)
				}
			}
			for _, secret := range []string{bodySecret, choiceSecret, modelSecret, metadataSecret} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
			if test.wantStatus != 0 {
				var statusErr *StatusError
				if !errors.As(err, &statusErr) || statusErr.StatusCode != test.wantStatus || statusErr.Detail != "provider request failed" {
					t.Fatalf("status error=%+v err=%v", statusErr, err)
				}
			}
		})
	}
}

func TestValidAttemptLedgerAcceptsOmittedOrExactOptionalDetail(t *testing.T) {
	t.Parallel()
	config := validConfig(func(string) error { return nil })
	var wire structuredResponse
	if !validAttemptLedger(wire, config) {
		t.Fatal("omitted optional attempt detail was rejected")
	}
	wire.Metadata.Attempts = append(wire.Metadata.Attempts, struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Status   int    `json:"status"`
	}{Provider: config.UpstreamProvider, Model: config.ResolvedModel, Status: 200})
	if !validAttemptLedger(wire, config) {
		t.Fatal("exact attempt detail was rejected")
	}
	wire.Metadata.Attempts[0].Provider = "Other"
	if validAttemptLedger(wire, config) {
		t.Fatal("mismatched attempt detail was accepted")
	}
}

func validConfig(reserve func(string) error) Config {
	return Config{
		APIKey: "secret", Model: "vendor/model", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider",
		SchemaName: "contract", Schema: map[string]any{"type": "object"},
		SystemPrompt: "system", Content: "content", MaxTokens: 32,
		MaxChargeNanoUSD: 2_000_000, Title: "contract test", Reserve: reserve,
	}
}

func validResponse() []byte {
	return []byte(`{"id":"generation","model":"vendor/model","choices":[{"message":{"content":"{\"ok\":true}","reasoning":""}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.001},"openrouter_metadata":{"attempt":1,"attempts":[{"provider":"Pinned Provider","model":"vendor/model-2026","status":200}],"endpoints":{"available":[{"provider":"Pinned Provider","model":"vendor/model-2026","selected":true}]}}}`)
}

func response(statusCode int, body []byte) *http.Response {
	return &http.Response{StatusCode: statusCode, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
