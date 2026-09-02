package fillerbakeoff

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestFetchOpenRouterSnapshotLocksEndpointIdentityPriceCapabilityAndZDR(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing snapshot authorization")
		}
		if request.Header.Get("X-OpenRouter-Title") != "Loomarr filler certification" || request.Header.Get("HTTP-Referer") != "https://github.com/loomarr/loomarr" {
			t.Error("missing snapshot client identity")
		}
		switch request.URL.Path {
		case "/models":
			_, _ = io.WriteString(writer, `{"data":[{"id":"vendor/model-1","canonical_slug":"vendor/model-1-20260826","name":"Model One","created":1}]}`)
		case "/endpoints/zdr":
			_, _ = io.WriteString(writer, `{"data":[`+snapshotEndpointFixture("vendor/model-1", "Pinned Provider", "pinned-provider/variant")+`]}`)
		case "/models/vendor/model-1/endpoints":
			_, _ = io.WriteString(writer, `{"data":{"id":"vendor/model-1","name":"Model One","created":1,"architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},"endpoints":[`+snapshotEndpointFixture("vendor/model-1", "Pinned Provider", "pinned-provider/variant")+`]}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	retrievedAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	snapshot, err := FetchOpenRouterSnapshot(context.Background(), OpenRouterSnapshotConfig{
		BaseURL: server.URL, APIKey: "secret", Models: []string{"vendor/model-1"}, RetrievedAt: retrievedAt,
		Client: server.Client(), AllowInsecureTestURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || snapshot.Requests != 3 || snapshot.ResponseBytes <= 0 || len(snapshot.Models) != 1 || snapshot.Models[0].CanonicalSlug != "vendor/model-1-20260826" || len(snapshot.Models[0].Endpoints) != 1 {
		t.Fatalf("snapshot envelope = %#v requests=%d", snapshot, requests)
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/models":
			if got := request.URL.Query().Get("output_modalities"); got != "transcription" {
				t.Fatalf("output_modalities = %q", got)
			}
			_, _ = io.WriteString(writer, `{"data":[{"id":"openai/whisper-large-v3","canonical_slug":"openai/whisper-large-v3","name":"Whisper","created":1}]}`)
		case "/endpoints/zdr":
			_, _ = io.WriteString(writer, `{"data":[`+strings.Replace(snapshotEndpointFixture("openai/whisper-large-v3", "Pinned Provider", "pinned-provider"), `"context_length":8192`, `"context_length":0`, 1)+`]}`)
		case "/models/openai/whisper-large-v3/endpoints":
			_, _ = io.WriteString(writer, `{"data":{"id":"openai/whisper-large-v3","name":"Whisper","created":1,"architecture":{"input_modalities":["audio"],"output_modalities":["transcription"]},"endpoints":[`+strings.Replace(snapshotEndpointFixture("openai/whisper-large-v3", "Pinned Provider", "pinned-provider"), `"context_length":8192`, `"context_length":0`, 1)+`]}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	snapshot, err := FetchOpenRouterSnapshot(context.Background(), OpenRouterSnapshotConfig{
		BaseURL: server.URL, APIKey: "secret", Models: []string{"openai/whisper-large-v3"}, OutputModality: "transcription", RetrievedAt: time.Now().UTC(), Client: server.Client(), AllowInsecureTestURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Models[0].InputModalities, []string{"audio"}) || !slices.Equal(snapshot.Models[0].OutputModalities, []string{"transcription"}) {
		t.Fatalf("transcription modalities = %+v", snapshot.Models[0])
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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/again", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := FetchOpenRouterSnapshot(context.Background(), OpenRouterSnapshotConfig{
		BaseURL: server.URL, APIKey: "secret", Models: []string{"vendor/model"}, RetrievedAt: time.Now().UTC(), Client: server.Client(), AllowInsecureTestURL: true,
	})
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("redirect error = %v", err)
	}
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
