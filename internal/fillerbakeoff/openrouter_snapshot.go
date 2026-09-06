package fillerbakeoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/httpx"
)

const (
	OpenRouterSnapshotSchemaVersion = 2
	maxSnapshotModels               = 16
	maxSnapshotEndpoints            = 256
	maxSnapshotResponseBytes        = 8 << 20
	maxSnapshotTotalBytes           = 32 << 20
	maxSnapshotAge                  = 24 * time.Hour
)

// OpenRouterSnapshot is the immutable capability, endpoint-price, and ZDR
// evidence used to bind one paid certification run.
type OpenRouterSnapshot struct {
	SchemaVersion int                       `json:"schemaVersion"`
	SourceBaseURL string                    `json:"sourceBaseUrl"`
	RetrievedAt   time.Time                 `json:"retrievedAt"`
	Requests      int                       `json:"requests"`
	ResponseBytes int64                     `json:"responseBytes"`
	Models        []OpenRouterModelSnapshot `json:"models"`
}

type OpenRouterModelSnapshot struct {
	ID               string                       `json:"id"`
	CanonicalSlug    string                       `json:"canonicalSlug"`
	Name             string                       `json:"name"`
	Created          int64                        `json:"created"`
	InputModalities  []string                     `json:"inputModalities"`
	OutputModalities []string                     `json:"outputModalities"`
	Endpoints        []OpenRouterEndpointSnapshot `json:"endpoints"`
}

type OpenRouterEndpointSnapshot struct {
	Name                  string            `json:"name"`
	ModelID               string            `json:"modelId"`
	ProviderName          string            `json:"providerName"`
	ProviderSlug          string            `json:"providerSlug"`
	Quantization          string            `json:"quantization"`
	ContextLength         int64             `json:"contextLength"`
	MaxCompletionTokens   int64             `json:"maxCompletionTokens,omitempty"`
	MaxPromptTokens       int64             `json:"maxPromptTokens,omitempty"`
	SupportedParameters   []string          `json:"supportedParameters"`
	Pricing               map[string]string `json:"pricing"`
	Status                int               `json:"status"`
	ZDR                   bool              `json:"zdr"`
	SupportsImplicitCache bool              `json:"supportsImplicitCaching"`
}

type OpenRouterSnapshotConfig struct {
	BaseURL              string
	APIKey               string
	Models               []string
	OutputModality       string
	RetrievedAt          time.Time
	Client               *http.Client
	AllowInsecureTestURL bool
}

type openRouterEndpointsResponse struct {
	Data struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Created      int64  `json:"created"`
		Architecture struct {
			InputModalities  []string `json:"input_modalities"`
			OutputModalities []string `json:"output_modalities"`
		} `json:"architecture"`
		Endpoints []openRouterEndpointWire `json:"endpoints"`
	} `json:"data"`
}

type openRouterModelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		CanonicalSlug string `json:"canonical_slug"`
		Name          string `json:"name"`
		Created       int64  `json:"created"`
	} `json:"data"`
}

type openRouterZDRResponse struct {
	Data []openRouterEndpointWire `json:"data"`
}

type openRouterEndpointWire struct {
	Name                    string                     `json:"name"`
	ModelID                 string                     `json:"model_id"`
	ProviderName            string                     `json:"provider_name"`
	Tag                     string                     `json:"tag"`
	Quantization            string                     `json:"quantization"`
	ContextLength           int64                      `json:"context_length"`
	MaxCompletionTokens     int64                      `json:"max_completion_tokens"`
	MaxPromptTokens         int64                      `json:"max_prompt_tokens"`
	SupportedParameters     []string                   `json:"supported_parameters"`
	Pricing                 map[string]json.RawMessage `json:"pricing"`
	Status                  int                        `json:"status"`
	SupportsImplicitCaching bool                       `json:"supports_implicit_caching"`
}

// FetchOpenRouterSnapshot performs one bounded model-catalog request, one
// bounded ZDR-list request, and one bounded endpoint request per requested
// model. It performs no inference.
func FetchOpenRouterSnapshot(ctx context.Context, config OpenRouterSnapshotConfig) (OpenRouterSnapshot, error) {
	baseURL, client, err := openRouterMetadataTransport(config)
	if err != nil {
		return OpenRouterSnapshot{}, err
	}
	models, err := validateSnapshotModels(config.Models)
	if err != nil {
		return OpenRouterSnapshot{}, err
	}
	if config.RetrievedAt.IsZero() {
		return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter snapshot requires an explicit retrieval time")
	}
	snapshot := OpenRouterSnapshot{SchemaVersion: OpenRouterSnapshotSchemaVersion, SourceBaseURL: baseURL, RetrievedAt: config.RetrievedAt.UTC()}
	var totalBytes int64
	var catalog openRouterModelsResponse
	catalogURL := baseURL + "/models"
	if config.OutputModality != "" {
		if !validOpenRouterCatalogModality(config.OutputModality) {
			return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter snapshot output modality %q is invalid", config.OutputModality)
		}
		query := url.Values{"output_modalities": []string{config.OutputModality}}
		catalogURL += "?" + query.Encode()
	}
	read, err := getOpenRouterJSON(ctx, client, catalogURL, config.APIKey, &catalog)
	if err != nil {
		return OpenRouterSnapshot{}, fmt.Errorf("fetch OpenRouter model catalog: %w", err)
	}
	snapshot.Requests++
	totalBytes += read
	catalogModels := make(map[string]struct {
		canonicalSlug string
		name          string
		created       int64
	}, len(catalog.Data))
	for _, model := range catalog.Data {
		if _, duplicate := catalogModels[model.ID]; duplicate {
			return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter model catalog repeats %q", model.ID)
		}
		catalogModels[model.ID] = struct {
			canonicalSlug string
			name          string
			created       int64
		}{model.CanonicalSlug, model.Name, model.Created}
	}
	var zdr openRouterZDRResponse
	read, err = getOpenRouterJSON(ctx, client, baseURL+"/endpoints/zdr", config.APIKey, &zdr)
	if err != nil {
		return OpenRouterSnapshot{}, fmt.Errorf("fetch OpenRouter ZDR endpoints: %w", err)
	}
	snapshot.Requests++
	totalBytes += read
	zdrKeys := make(map[string]struct{}, len(zdr.Data))
	for _, endpoint := range zdr.Data {
		zdrKeys[openRouterEndpointKey(endpoint)] = struct{}{}
	}
	for _, modelID := range models {
		catalogModel, ok := catalogModels[modelID]
		if !ok || catalogModel.canonicalSlug == "" {
			return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter model catalog omitted canonical identity for %q", modelID)
		}
		var response openRouterEndpointsResponse
		modelPath := strings.Join([]string{url.PathEscape(strings.Split(modelID, "/")[0]), url.PathEscape(strings.Split(modelID, "/")[1])}, "/")
		read, err := getOpenRouterJSON(ctx, client, baseURL+"/models/"+modelPath+"/endpoints", config.APIKey, &response)
		if err != nil {
			return OpenRouterSnapshot{}, fmt.Errorf("fetch OpenRouter model %q endpoints: %w", modelID, err)
		}
		snapshot.Requests++
		if totalBytes > maxSnapshotTotalBytes-read {
			return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter snapshot responses exceed %d-byte total ceiling", maxSnapshotTotalBytes)
		}
		totalBytes += read
		if response.Data.ID != modelID || response.Data.Name != catalogModel.name || response.Data.Created != catalogModel.created {
			return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter endpoint response for %q returned model %q", modelID, response.Data.ID)
		}
		model := OpenRouterModelSnapshot{
			ID: modelID, CanonicalSlug: catalogModel.canonicalSlug, Name: response.Data.Name, Created: response.Data.Created,
			InputModalities:  sortedUnique(response.Data.Architecture.InputModalities),
			OutputModalities: sortedUnique(response.Data.Architecture.OutputModalities),
		}
		if len(response.Data.Endpoints) == 0 || len(response.Data.Endpoints) > maxSnapshotEndpoints {
			return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter model %q returned an invalid endpoint count", modelID)
		}
		for _, wire := range response.Data.Endpoints {
			pricing, err := normalizeOpenRouterPricing(wire.Pricing)
			if err != nil {
				return OpenRouterSnapshot{}, fmt.Errorf("OpenRouter model %q endpoint %q pricing: %w", modelID, wire.Tag, err)
			}
			_, isZDR := zdrKeys[openRouterEndpointKey(wire)]
			model.Endpoints = append(model.Endpoints, OpenRouterEndpointSnapshot{
				Name: wire.Name, ModelID: wire.ModelID, ProviderName: wire.ProviderName, ProviderSlug: wire.Tag,
				Quantization: wire.Quantization, ContextLength: wire.ContextLength,
				MaxCompletionTokens: wire.MaxCompletionTokens, MaxPromptTokens: wire.MaxPromptTokens,
				SupportedParameters: sortedUnique(wire.SupportedParameters), Pricing: pricing,
				Status: wire.Status, ZDR: isZDR, SupportsImplicitCache: wire.SupportsImplicitCaching,
			})
		}
		slices.SortFunc(model.Endpoints, func(a, b OpenRouterEndpointSnapshot) int {
			if n := strings.Compare(a.ProviderSlug, b.ProviderSlug); n != 0 {
				return n
			}
			return strings.Compare(a.Name, b.Name)
		})
		snapshot.Models = append(snapshot.Models, model)
	}
	snapshot.ResponseBytes = totalBytes
	if err := ValidateOpenRouterSnapshot(snapshot); err != nil {
		return OpenRouterSnapshot{}, err
	}
	return snapshot, nil
}

func validOpenRouterCatalogModality(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return true
}

func openRouterMetadataTransport(config OpenRouterSnapshotConfig) (string, *http.Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = OpenRouterBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", nil, fmt.Errorf("OpenRouter snapshot requires an HTTP API base")
	}
	loopbackTest := config.AllowInsecureTestURL && loopbackHost(parsed.Hostname())
	if !loopbackTest && (parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "openrouter.ai") || parsed.Path != "/api/v1") {
		return "", nil, fmt.Errorf("OpenRouter snapshot requires the canonical HTTPS API base outside a loopback test server")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return "", nil, fmt.Errorf("OpenRouter snapshot requires an API key for the ZDR endpoint list")
	}
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-openrouter-snapshot", httpx.TimeoutLLM)
	}
	copy := *client
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return baseURL, &copy, nil
}

func validateSnapshotModels(models []string) ([]string, error) {
	if len(models) == 0 || len(models) > maxSnapshotModels {
		return nil, fmt.Errorf("OpenRouter snapshot requires between one and %d models", maxSnapshotModels)
	}
	result := append([]string(nil), models...)
	for _, model := range result {
		parts := strings.Split(model, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(strings.ToLower(model), "latest") || len(model) > maxFieldBytes {
			return nil, fmt.Errorf("OpenRouter snapshot model %q is not one concrete namespaced ID", model)
		}
	}
	slices.Sort(result)
	if len(sortedUnique(result)) != len(result) {
		return nil, fmt.Errorf("OpenRouter snapshot model IDs must be unique")
	}
	return result, nil
}

func getOpenRouterJSON(ctx context.Context, client *http.Client, endpoint, apiKey string, target any) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("HTTP-Referer", "https://github.com/loomarr/loomarr")
	request.Header.Set("X-OpenRouter-Title", "Loomarr filler certification")
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxSnapshotResponseBytes+1))
	if err != nil {
		return 0, err
	}
	if len(raw) > maxSnapshotResponseBytes {
		return 0, fmt.Errorf("response exceeded %d-byte ceiling", maxSnapshotResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return int64(len(raw)), fmt.Errorf("status %d: %s", response.StatusCode, boundedMessage(raw))
	}
	if err := decodeProviderJSON(raw, target); err != nil {
		return int64(len(raw)), err
	}
	return int64(len(raw)), nil
}

func openRouterEndpointKey(endpoint openRouterEndpointWire) string {
	return endpoint.ModelID + "\x00" + endpoint.Tag + "\x00" + endpoint.ProviderName + "\x00" + endpoint.Name
}

func normalizeOpenRouterPricing(raw map[string]json.RawMessage) (map[string]string, error) {
	pricing := make(map[string]string, len(raw))
	for name, value := range raw {
		if len(name) > maxFieldBytes {
			return nil, fmt.Errorf("price key is too long")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		var text string
		if err := json.Unmarshal(value, &text); err == nil {
			pricing[name] = text
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		var number json.Number
		if err := decoder.Decode(&number); err != nil {
			return nil, fmt.Errorf("price %q is not exact numeric text", name)
		}
		pricing[name] = number.String()
	}
	return pricing, nil
}

func sortedUnique(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return slices.Compact(result)
}

func OpenRouterSnapshotSHA256(snapshot OpenRouterSnapshot) string {
	data, err := json.Marshal(snapshot)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ValidateOpenRouterSnapshot(snapshot OpenRouterSnapshot) error {
	parsedSource, sourceErr := url.Parse(snapshot.SourceBaseURL)
	if snapshot.SchemaVersion != OpenRouterSnapshotSchemaVersion || sourceErr != nil || parsedSource.Host == "" || snapshot.RetrievedAt.IsZero() || snapshot.RetrievedAt.Location() != time.UTC {
		return fmt.Errorf("OpenRouter snapshot requires schema %d and a UTC retrieval time", OpenRouterSnapshotSchemaVersion)
	}
	if len(snapshot.Models) == 0 || len(snapshot.Models) > maxSnapshotModels || snapshot.Requests != len(snapshot.Models)+2 || snapshot.ResponseBytes <= 0 || snapshot.ResponseBytes > maxSnapshotTotalBytes {
		return fmt.Errorf("OpenRouter snapshot has invalid bounded request, response, or model counts")
	}
	modelIDs := make([]string, 0, len(snapshot.Models))
	for _, model := range snapshot.Models {
		modelIDs = append(modelIDs, model.ID)
		canonicalOwner, canonicalName, canonical := strings.Cut(model.CanonicalSlug, "/")
		if model.ID == "" || !canonical || canonicalOwner == "" || canonicalName == "" || strings.Contains(strings.ToLower(model.CanonicalSlug), "latest") || model.Name == "" || model.Created <= 0 || len(model.Endpoints) == 0 || len(model.Endpoints) > maxSnapshotEndpoints || !canonicalStrings(model.InputModalities) || !canonicalStrings(model.OutputModalities) || !validOpenRouterOutputModalities(model.OutputModalities) {
			return fmt.Errorf("OpenRouter snapshot model %q has an invalid bounded identity or architecture", model.ID)
		}
		seenEndpoints := make(map[string]struct{}, len(model.Endpoints))
		previousEndpoint := ""
		requiresContext := slices.Contains(model.OutputModalities, "text")
		for _, endpoint := range model.Endpoints {
			key := endpoint.ProviderSlug + "\x00" + endpoint.Name
			if key < previousEndpoint {
				return fmt.Errorf("OpenRouter snapshot model %q endpoints are not canonical", model.ID)
			}
			previousEndpoint = key
			if _, exists := seenEndpoints[key]; exists {
				return fmt.Errorf("OpenRouter snapshot model %q repeats endpoint %q", model.ID, endpoint.ProviderSlug)
			}
			seenEndpoints[key] = struct{}{}
			if endpoint.Name == "" || endpoint.ModelID != model.ID || endpoint.ProviderName == "" || endpoint.ProviderSlug == "" || requiresContext && endpoint.ContextLength <= 0 || !requiresContext && endpoint.ContextLength < 0 || endpoint.MaxCompletionTokens < 0 || endpoint.MaxPromptTokens < 0 || !canonicalStrings(endpoint.SupportedParameters) || len(endpoint.Pricing) == 0 || len(endpoint.Pricing) > 32 {
				return fmt.Errorf("OpenRouter snapshot model %q has an invalid endpoint", model.ID)
			}
			for name, price := range endpoint.Pricing {
				if name == "" || len(name) > maxFieldBytes || price == "" || len(price) > 128 {
					return fmt.Errorf("OpenRouter snapshot endpoint %q has invalid bounded pricing", endpoint.ProviderSlug)
				}
				if _, err := fillereval.USDToNanoCeil(price); err != nil {
					return fmt.Errorf("OpenRouter snapshot endpoint %q price %q: %w", endpoint.ProviderSlug, name, err)
				}
			}
		}
	}
	if !canonicalStrings(modelIDs) {
		return fmt.Errorf("OpenRouter snapshot models are not canonical")
	}
	return nil
}

func validOpenRouterOutputModalities(modalities []string) bool {
	return slices.Contains(modalities, "text") || slices.Contains(modalities, "transcription")
}

func ValidateOpenRouterRunSnapshot(run fillereval.RunIdentity, routes []Route, snapshot OpenRouterSnapshot) error {
	if err := ValidateOpenRouterSnapshot(snapshot); err != nil {
		return err
	}
	digest := OpenRouterSnapshotSHA256(snapshot)
	if snapshot.SourceBaseURL != OpenRouterBaseURL {
		return fmt.Errorf("OpenRouter certification requires a snapshot from the canonical API base")
	}
	if run.CapabilitySnapshot != digest || run.PriceSnapshot != digest {
		return fmt.Errorf("OpenRouter run capability and price identities must equal snapshot digest %s", digest)
	}
	age := run.GeneratedAt.Sub(snapshot.RetrievedAt)
	if age < 0 || age > maxSnapshotAge {
		return fmt.Errorf("OpenRouter run time is outside the snapshot's 24-hour certification window")
	}
	for _, route := range routes {
		if route.Provider != "openrouter" {
			continue
		}
		model, ok := snapshotModel(snapshot, route.Model)
		if !ok {
			return fmt.Errorf("OpenRouter route model %q is absent from the locked snapshot", route.Model)
		}
		if route.ResolvedModel != model.CanonicalSlug {
			return fmt.Errorf("OpenRouter route model %q does not bind canonical revision %q", route.Model, model.CanonicalSlug)
		}
		endpoint, ok := snapshotEndpoint(model, route.UpstreamProviderSlug, route.UpstreamProvider)
		if !ok {
			return fmt.Errorf("OpenRouter route %q endpoint identity is absent from the locked snapshot", route.Rung)
		}
		if !endpoint.ZDR || endpoint.Status != 0 || endpoint.MaxCompletionTokens < maxOpenRouterOutputTokens || !slices.Contains(endpoint.SupportedParameters, "response_format") || !slices.Contains(endpoint.SupportedParameters, "structured_outputs") {
			return fmt.Errorf("OpenRouter route %q endpoint is not live, ZDR, and strict-output compatible", route.Rung)
		}
		for _, modality := range route.Modalities {
			if !slices.Contains(model.InputModalities, modality) {
				return fmt.Errorf("OpenRouter route %q modality %q is absent from the locked snapshot", route.Rung, modality)
			}
		}
		if endpoint.Pricing["prompt"] == "" || endpoint.Pricing["completion"] == "" {
			return fmt.Errorf("OpenRouter route %q endpoint lacks prompt or completion pricing", route.Rung)
		}
	}
	return nil
}

func canonicalStrings(values []string) bool {
	if len(values) == 0 || !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if value == "" || len(value) > maxFieldBytes || (index > 0 && values[index-1] == value) {
			return false
		}
	}
	return true
}

func snapshotModel(snapshot OpenRouterSnapshot, id string) (OpenRouterModelSnapshot, bool) {
	for _, model := range snapshot.Models {
		if model.ID == id {
			return model, true
		}
	}
	return OpenRouterModelSnapshot{}, false
}

func snapshotEndpoint(model OpenRouterModelSnapshot, slug, provider string) (OpenRouterEndpointSnapshot, bool) {
	for _, endpoint := range model.Endpoints {
		if endpoint.ProviderSlug == slug && endpoint.ProviderName == provider {
			return endpoint, true
		}
	}
	return OpenRouterEndpointSnapshot{}, false
}
