package fillerreview

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallOpenRouterStructuredReservesExactRequestBeforeHTTP(t *testing.T) {
	t.Parallel()
	var reservedHash string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if reservedHash == "" || reservedHash != hashBytes(body) {
			t.Errorf("request was not durably reserved before HTTP: reserved=%q actual=%q", reservedHash, hashBytes(body))
		}
		if request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("X-OpenRouter-Metadata") != "enabled" || request.Header.Get("X-OpenRouter-Title") != "contract test" {
			t.Errorf("request headers=%v", request.Header)
		}
		_, _ = w.Write(validOpenRouterStructuredResponse())
	}))
	defer server.Close()

	result, err := callOpenRouterStructured(t.Context(), server.Client(), server.URL, openRouterStructuredCallConfig{
		APIKey: "secret", Model: "vendor/model", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider",
		SchemaName: "contract", Schema: map[string]any{"type": "object"},
		SystemPrompt: "system", Content: "content", Images: []string{"aW1hZ2U="},
		Videos:    []openRouterStructuredVideo{{MIMEType: "video/mp4", Base64: "dmlkZW8="}},
		MaxTokens: 32, MaxChargeNanoUSD: 2_000_000, DisableReasoning: true,
		Title: "contract test", Reserve: func(requestSHA256 string) error {
			reservedHash = requestSHA256
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestSHA256 != reservedHash || result.ResponseSHA256 != hashBytes(result.RawResponse) || !result.ChargeKnown || result.ChargedNanoUSD != 1_000_000 || result.StructuredOutput != `{"ok":true}` {
		t.Fatalf("result=%+v", result)
	}
}

func TestCallOpenRouterStructuredRequiresReservationBeforeHTTP(t *testing.T) {
	t.Parallel()
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}
	result, err := callOpenRouterStructured(t.Context(), client, "https://openrouter.ai/api/v1", openRouterStructuredCallConfig{})
	if err == nil || !strings.Contains(err.Error(), "durable reservation") || called || result.RequestSHA256 == "" {
		t.Fatalf("result=%+v err=%v called=%t", result, err, called)
	}
}

func TestCallOpenRouterStructuredRetainsNonOKResponseAuthority(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"error":"temporarily unavailable"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	result, err := callOpenRouterStructured(t.Context(), server.Client(), server.URL, validOpenRouterStructuredConfig(func(string) error { return nil }))
	var statusErr *openRouterStructuredStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable || result.ResponseSHA256 != hashBytes(raw) || !bytes.Equal(result.RawResponse, raw) || result.ChargeKnown {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCallOpenRouterStructuredFailsClosedAfterSettlement(t *testing.T) {
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
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			result, err := callOpenRouterStructured(t.Context(), server.Client(), server.URL, validOpenRouterStructuredConfig(func(string) error { return nil }))
			if err == nil || !strings.Contains(err.Error(), test.want) || result.ResponseSHA256 == "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCallOpenRouterStructuredRejectsOversizedResponseAfterReservation(t *testing.T) {
	t.Parallel()
	reserved := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxReviewResponseBytes+1))
	}))
	defer server.Close()
	result, err := callOpenRouterStructured(t.Context(), server.Client(), server.URL, validOpenRouterStructuredConfig(func(string) error {
		reserved = true
		return nil
	}))
	if err == nil || !strings.Contains(err.Error(), "byte ceiling") || !reserved || result.RequestSHA256 == "" || result.ResponseSHA256 != "" || len(result.RawResponse) != 0 {
		t.Fatalf("result=%+v err=%v reserved=%t", result, err, reserved)
	}
}

func validOpenRouterStructuredConfig(reserve func(string) error) openRouterStructuredCallConfig {
	return openRouterStructuredCallConfig{
		APIKey: "secret", Model: "vendor/model", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider",
		SchemaName: "contract", Schema: map[string]any{"type": "object"},
		SystemPrompt: "system", Content: "content", MaxTokens: 32,
		MaxChargeNanoUSD: 2_000_000, Title: "contract test", Reserve: reserve,
	}
}

func validOpenRouterStructuredResponse() []byte {
	return []byte(`{"id":"generation","model":"vendor/model","choices":[{"message":{"content":"{\"ok\":true}","reasoning":""}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.001},"openrouter_metadata":{"attempt":1,"attempts":[{"provider":"Pinned Provider","model":"vendor/model-2026","status":200}],"endpoints":{"available":[{"provider":"Pinned Provider","model":"vendor/model-2026","selected":true}]}}}`)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
