package fillerbakeoff

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

func TestFetchOpenRouterSnapshotLocksEndpointIdentityPriceCapabilityAndZDR(t *testing.T) {
	t.Parallel()
	transport := httpfixture.NewScriptedTransport(
		httpfixture.Step{Response: snapshotResponse(http.StatusOK, `{"data":[{"id":"vendor/model-1","canonical_slug":"vendor/model-1-20260826","name":"Model One","created":1}]}`)},
		httpfixture.Step{Response: snapshotResponse(http.StatusOK, `{"data":[`+snapshotEndpointFixture("vendor/model-1", "Pinned Provider", "pinned-provider/variant")+`]}`)},
		httpfixture.Step{Response: snapshotResponse(http.StatusOK, `{"data":{"id":"vendor/model-1","name":"Model One","created":1,"architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},"endpoints":[`+snapshotEndpointFixture("vendor/model-1", "Pinned Provider", "pinned-provider/variant")+`]}}`)},
	)
	retrievedAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	snapshot, err := FetchOpenRouterSnapshot(context.Background(), OpenRouterSnapshotConfig{
		BaseURL: OpenRouterBaseURL, APIKey: "secret", Models: []string{"vendor/model-1"}, RetrievedAt: retrievedAt,
		Client: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transport.Calls() != 3 || snapshot.Requests != 3 || snapshot.ResponseBytes <= 0 || len(snapshot.Models) != 1 || snapshot.Models[0].CanonicalSlug != "vendor/model-1-20260826" || len(snapshot.Models[0].Endpoints) != 1 {
		t.Fatalf("snapshot envelope = %#v requests=%d", snapshot, transport.Calls())
	}
	for index, request := range transport.Requests() {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request %d authorization = %q", index, request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-OpenRouter-Title") != "Loomarr filler certification" || request.Header.Get("HTTP-Referer") != "https://github.com/loomarr/loomarr" {
			t.Errorf("request %d client identity headers = %+v", index, request.Header)
		}
	}
	if got := []string{transport.Requests()[0].URL, transport.Requests()[1].URL, transport.Requests()[2].URL}; !slices.Equal(got, []string{OpenRouterBaseURL + "/models", OpenRouterBaseURL + "/endpoints/zdr", OpenRouterBaseURL + "/models/vendor/model-1/endpoints"}) {
		t.Fatalf("request URLs = %v", got)
	}
	endpoint := snapshot.Models[0].Endpoints[0]
	if endpoint.ProviderSlug != "pinned-provider/variant" || endpoint.ProviderName != "Pinned Provider" || !endpoint.ZDR || endpoint.Pricing["prompt"] != "0.000001" || endpoint.Pricing["discount"] != "0.25" || !slices.Equal(endpoint.SupportedParameters, []string{"response_format", "structured_outputs"}) {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	if len(OpenRouterSnapshotSHA256(snapshot)) != 64 {
		t.Fatal("snapshot has no digest")
	}
}

func TestFetchOpenRouterSnapshotFiltersTheTranscriptionCatalog(t *testing.T) {
	t.Parallel()
	transport := httpfixture.NewScriptedTransport(
		httpfixture.Step{Response: snapshotResponse(http.StatusOK, `{"data":[{"id":"openai/whisper-large-v3","canonical_slug":"openai/whisper-large-v3","name":"Whisper","created":1}]}`)},
		httpfixture.Step{Response: snapshotResponse(http.StatusOK, `{"data":[`+strings.Replace(snapshotEndpointFixture("openai/whisper-large-v3", "Pinned Provider", "pinned-provider"), `"context_length":8192`, `"context_length":0`, 1)+`]}`)},
		httpfixture.Step{Response: snapshotResponse(http.StatusOK, `{"data":{"id":"openai/whisper-large-v3","name":"Whisper","created":1,"architecture":{"input_modalities":["audio"],"output_modalities":["transcription"]},"endpoints":[`+strings.Replace(snapshotEndpointFixture("openai/whisper-large-v3", "Pinned Provider", "pinned-provider"), `"context_length":8192`, `"context_length":0`, 1)+`]}}`)},
	)
	snapshot, err := FetchOpenRouterSnapshot(context.Background(), OpenRouterSnapshotConfig{
		BaseURL: OpenRouterBaseURL, APIKey: "secret", Models: []string{"openai/whisper-large-v3"}, OutputModality: "transcription", RetrievedAt: time.Now().UTC(), Client: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Models[0].InputModalities, []string{"audio"}) || !slices.Equal(snapshot.Models[0].OutputModalities, []string{"transcription"}) {
		t.Fatalf("transcription modalities = %+v", snapshot.Models[0])
	}
	if requests := transport.Requests(); len(requests) != 3 || requests[0].URL != OpenRouterBaseURL+"/models?output_modalities=transcription" || requests[1].URL != OpenRouterBaseURL+"/endpoints/zdr" || requests[2].URL != OpenRouterBaseURL+"/models/openai/whisper-large-v3/endpoints" {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestValidateOpenRouterRunSnapshotBindsDigestFreshnessAndRoute(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	digest := OpenRouterSnapshotSHA256(snapshot)
	run := fillereval.RunIdentity{CapabilitySnapshot: digest, PriceSnapshot: digest, GeneratedAt: snapshot.RetrievedAt.Add(time.Hour)}
	route := Route{Provider: "openrouter", Model: "vendor/model-1", ResolvedModel: "vendor/model-1-20260826", Rung: "text", UpstreamProviderSlug: "pinned-provider/variant", UpstreamProvider: "Pinned Provider", Modalities: []string{"text"}}
	if err := ValidateOpenRouterRunSnapshot(run, []Route{route}, snapshot); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*fillereval.RunIdentity, *Route, *OpenRouterSnapshot){
		"digest": func(run *fillereval.RunIdentity, _ *Route, _ *OpenRouterSnapshot) {
			run.PriceSnapshot = strings.Repeat("0", 64)
		},
		"stale": func(run *fillereval.RunIdentity, _ *Route, snapshot *OpenRouterSnapshot) {
			run.GeneratedAt = snapshot.RetrievedAt.Add(25 * time.Hour)
		},
		"selector": func(_ *fillereval.RunIdentity, route *Route, _ *OpenRouterSnapshot) {
			route.UpstreamProviderSlug = "other"
		},
		"provider": func(_ *fillereval.RunIdentity, route *Route, _ *OpenRouterSnapshot) { route.UpstreamProvider = "Other" },
		"model revision": func(_ *fillereval.RunIdentity, route *Route, _ *OpenRouterSnapshot) {
			route.ResolvedModel = "vendor/model-1-other"
		},
		"modality": func(_ *fillereval.RunIdentity, route *Route, _ *OpenRouterSnapshot) {
			route.Modalities = []string{"video"}
		},
		"privacy": func(_ *fillereval.RunIdentity, _ *Route, snapshot *OpenRouterSnapshot) {
			snapshot.Models[0].Endpoints[0].ZDR = false
		},
		"parameters": func(_ *fillereval.RunIdentity, _ *Route, snapshot *OpenRouterSnapshot) {
			snapshot.Models[0].Endpoints[0].SupportedParameters = []string{"response_format"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedRun, changedRoute, changedSnapshot := run, route, snapshot
			changedSnapshot.Models = append([]OpenRouterModelSnapshot(nil), snapshot.Models...)
			changedSnapshot.Models[0].Endpoints = append([]OpenRouterEndpointSnapshot(nil), snapshot.Models[0].Endpoints...)
			mutate(&changedRun, &changedRoute, &changedSnapshot)
			if name != "digest" && name != "stale" {
				changedDigest := OpenRouterSnapshotSHA256(changedSnapshot)
				changedRun.CapabilitySnapshot, changedRun.PriceSnapshot = changedDigest, changedDigest
			}
			if err := ValidateOpenRouterRunSnapshot(changedRun, []Route{changedRoute}, changedSnapshot); err == nil {
				t.Fatal("invalid snapshot binding accepted")
			}
		})
	}
}

func TestValidateOpenRouterSnapshotPreservesInactiveSiblingRoutes(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	inactive := snapshot.Models[0].Endpoints[0]
	inactive.Name = "Inactive Provider | model"
	inactive.ProviderName = "Inactive Provider"
	inactive.ProviderSlug = "inactive-provider/variant"
	inactive.Status = -2
	snapshot.Models[0].Endpoints = append([]OpenRouterEndpointSnapshot{inactive}, snapshot.Models[0].Endpoints...)
	if err := ValidateOpenRouterSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	run := fillereval.RunIdentity{CapabilitySnapshot: OpenRouterSnapshotSHA256(snapshot), PriceSnapshot: OpenRouterSnapshotSHA256(snapshot), GeneratedAt: snapshot.RetrievedAt.Add(time.Hour)}
	route := Route{Provider: "openrouter", Model: "vendor/model-1", ResolvedModel: "vendor/model-1-20260826", Rung: "text", UpstreamProviderSlug: inactive.ProviderSlug, UpstreamProvider: inactive.ProviderName, Modalities: []string{"text"}}
	if err := ValidateOpenRouterRunSnapshot(run, []Route{route}, snapshot); err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("inactive selected route error = %v", err)
	}
}

func TestFetchOpenRouterSnapshotRejectsMissingZDRCredentialAndRedirect(t *testing.T) {
	t.Parallel()
	if _, err := FetchOpenRouterSnapshot(context.Background(), OpenRouterSnapshotConfig{Models: []string{"vendor/model"}, RetrievedAt: time.Now().UTC()}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("missing key error = %v", err)
	}
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{"/again"}}, Body: http.NoBody}})
	_, err := FetchOpenRouterSnapshot(context.Background(), OpenRouterSnapshotConfig{
		BaseURL: OpenRouterBaseURL, APIKey: "secret", Models: []string{"vendor/model"}, RetrievedAt: time.Now().UTC(), Client: &http.Client{Transport: transport},
	})
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("redirect error = %v", err)
	}
}

func snapshotResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func validOpenRouterSnapshot() OpenRouterSnapshot {
	return OpenRouterSnapshot{
		SchemaVersion: OpenRouterSnapshotSchemaVersion, SourceBaseURL: OpenRouterBaseURL, RetrievedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC), Requests: 3, ResponseBytes: 100,
		Models: []OpenRouterModelSnapshot{{
			ID: "vendor/model-1", CanonicalSlug: "vendor/model-1-20260826", Name: "Model One", Created: 1, InputModalities: []string{"image", "text"}, OutputModalities: []string{"text"},
			Endpoints: []OpenRouterEndpointSnapshot{{
				Name: "Pinned Provider | model", ModelID: "vendor/model-1", ProviderName: "Pinned Provider", ProviderSlug: "pinned-provider/variant",
				Quantization: "fp16", ContextLength: 8192, MaxCompletionTokens: 1024,
				SupportedParameters: []string{"response_format", "structured_outputs"}, Pricing: map[string]string{"completion": "0.000002", "prompt": "0.000001"}, ZDR: true,
			}},
		}},
	}
}

func snapshotEndpointFixture(model, provider, slug string) string {
	return `{"name":"` + provider + ` | model","model_id":"` + model + `","provider_name":"` + provider + `","tag":"` + slug + `","quantization":"fp16","context_length":8192,"max_completion_tokens":1024,"max_prompt_tokens":4096,"supported_parameters":["structured_outputs","response_format"],"pricing":{"prompt":"0.000001","completion":"0.000002","discount":0.25},"status":0,"supports_implicit_caching":false}`
}
