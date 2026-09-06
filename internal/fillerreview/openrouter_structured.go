package fillerreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
)

type openRouterStructuredRequest struct {
	Model          string                         `json:"model"`
	Messages       []openRouterStructuredMessage  `json:"messages"`
	Provider       openRouterStructuredRoute      `json:"provider"`
	ResponseFormat openRouterStructuredFormat     `json:"response_format"`
	Reasoning      *openRouterStructuredReasoning `json:"reasoning,omitempty"`
	MaxTokens      int                            `json:"max_tokens"`
}

type openRouterStructuredReasoning struct {
	Enabled bool `json:"enabled"`
}

type openRouterStructuredMessage struct {
	Role    string                     `json:"role"`
	Content []openRouterStructuredPart `json:"content"`
}

type openRouterStructuredPart struct {
	Type     string                        `json:"type"`
	Text     string                        `json:"text,omitempty"`
	ImageURL *openRouterStructuredMediaURL `json:"image_url,omitempty"`
	VideoURL *openRouterStructuredMediaURL `json:"video_url,omitempty"`
}

type openRouterStructuredMediaURL struct {
	URL string `json:"url"`
}

type openRouterStructuredRoute struct {
	Order             []string `json:"order"`
	AllowFallbacks    bool     `json:"allow_fallbacks"`
	RequireParameters bool     `json:"require_parameters"`
	DataCollection    string   `json:"data_collection"`
	ZDR               bool     `json:"zdr"`
}

type openRouterStructuredFormat struct {
	Type       string                         `json:"type"`
	JSONSchema openRouterStructuredJSONSchema `json:"json_schema"`
}

type openRouterStructuredJSONSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type openRouterStructuredResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
		} `json:"message"`
		Error *openRouterStructuredWireError `json:"error"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64       `json:"prompt_tokens"`
		CompletionTokens int64       `json:"completion_tokens"`
		Cost             json.Number `json:"cost"`
	} `json:"usage"`
	Metadata struct {
		Attempt   int `json:"attempt"`
		Endpoints struct {
			Available []struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Selected bool   `json:"selected"`
			} `json:"available"`
		} `json:"endpoints"`
		Attempts []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Status   int    `json:"status"`
		} `json:"attempts"`
	} `json:"openrouter_metadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type openRouterStructuredWireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type openRouterStructuredCallConfig struct {
	APIKey           string
	Model            string
	ResolvedModel    string
	UpstreamProvider string
	ProviderSlug     string
	SchemaName       string
	Schema           map[string]any
	SystemPrompt     string
	Content          string
	Images           []string
	Videos           []openRouterStructuredVideo
	MaxTokens        int
	MaxChargeNanoUSD int64
	DisableReasoning bool
	Title            string
	Reserve          func(string) error
}

type openRouterStructuredVideo struct {
	MIMEType string
	Base64   string
}

type openRouterStructuredCallResult struct {
	Wire             openRouterStructuredResponse
	RawResponse      []byte
	RequestSHA256    string
	ResponseSHA256   string
	ChargedNanoUSD   int64
	ChargeKnown      bool
	StructuredOutput string
}

type openRouterStructuredStatusError struct {
	StatusCode int
	Detail     string
}

func (e *openRouterStructuredStatusError) Error() string {
	return fmt.Sprintf("OpenRouter structured call returned status %d: %s", e.StatusCode, e.Detail)
}

// callOpenRouterStructured owns the exact paid transport contract shared by
// hosted blind review and temporal calibration. Callers own prompts and output
// semantics; this module owns route pinning, strict JSON mode, reservation,
// response bounds, charge settlement, and one-attempt identity.
func callOpenRouterStructured(ctx context.Context, client *http.Client, baseURL string, config openRouterStructuredCallConfig) (openRouterStructuredCallResult, error) {
	parts := []openRouterStructuredPart{{Type: "text", Text: config.Content}}
	for _, image := range config.Images {
		parts = append(parts, openRouterStructuredPart{Type: "image_url", ImageURL: &openRouterStructuredMediaURL{URL: "data:image/jpeg;base64," + image}})
	}
	for _, video := range config.Videos {
		if !validOpenRouterVideoMIME(video.MIMEType) || strings.TrimSpace(video.Base64) == "" {
			return openRouterStructuredCallResult{}, fmt.Errorf("OpenRouter structured video input has an invalid MIME type or empty payload")
		}
		parts = append(parts, openRouterStructuredPart{Type: "video_url", VideoURL: &openRouterStructuredMediaURL{URL: "data:" + video.MIMEType + ";base64," + video.Base64}})
	}
	payload := openRouterStructuredRequest{
		Model: config.Model,
		Messages: []openRouterStructuredMessage{
			{Role: "system", Content: []openRouterStructuredPart{{Type: "text", Text: config.SystemPrompt}}},
			{Role: "user", Content: parts},
		},
		Provider:       openRouterStructuredRoute{Order: []string{config.ProviderSlug}, RequireParameters: true, DataCollection: "deny", ZDR: true},
		ResponseFormat: openRouterStructuredFormat{Type: "json_schema", JSONSchema: openRouterStructuredJSONSchema{Name: config.SchemaName, Strict: true, Schema: config.Schema}},
		MaxTokens:      config.MaxTokens,
	}
	if config.DisableReasoning {
		payload.Reasoning = &openRouterStructuredReasoning{Enabled: false}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return openRouterStructuredCallResult{}, err
	}
	result := openRouterStructuredCallResult{RequestSHA256: hashBytes(body)}
	if config.Reserve == nil {
		return result, fmt.Errorf("OpenRouter structured call requires a durable reservation callback")
	}
	if err := config.Reserve(result.RequestSHA256); err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+config.APIKey)
	request.Header.Set("X-OpenRouter-Metadata", "enabled")
	request.Header.Set("HTTP-Referer", "https://github.com/loomarr/loomarr")
	request.Header.Set("X-OpenRouter-Title", config.Title)
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxReviewResponseBytes+1))
	if err != nil || len(raw) > maxReviewResponseBytes {
		return result, fmt.Errorf("OpenRouter structured response exceeded its byte ceiling")
	}
	result.ResponseSHA256 = hashBytes(raw)
	result.RawResponse = bytes.Clone(raw)
	if response.StatusCode != http.StatusOK {
		return result, &openRouterStructuredStatusError{StatusCode: response.StatusCode, Detail: boundedReviewMessage(raw)}
	}
	if err := decodeProviderReviewJSON(raw, &result.Wire); err != nil {
		return result, err
	}
	if result.Wire.Error != nil {
		return result, fmt.Errorf("OpenRouter structured call error: %s", strings.TrimSpace(result.Wire.Error.Message))
	}
	charged, err := fillereval.USDToNanoCeil(result.Wire.Usage.Cost.String())
	if err != nil || charged < 0 || charged > config.MaxChargeNanoUSD {
		return result, fmt.Errorf("OpenRouter structured call returned missing or out-of-reservation cost")
	}
	result.ChargedNanoUSD, result.ChargeKnown = charged, true
	if len(result.Wire.Choices) == 1 && result.Wire.Choices[0].Error != nil {
		wireError := result.Wire.Choices[0].Error
		status := wireError.Code
		if status < 100 || status > 599 {
			status = http.StatusBadGateway
		}
		return result, &openRouterStructuredStatusError{StatusCode: status, Detail: strings.TrimSpace(wireError.Message)}
	}
	if result.Wire.ID == "" || result.Wire.Model != config.Model || len(result.Wire.Choices) != 1 || result.Wire.Metadata.Attempt != 1 || !validStructuredAttemptLedger(result.Wire, config) || !selectedStructuredEndpoint(result.Wire, config) {
		return result, fmt.Errorf("OpenRouter structured response does not bind the requested one-attempt route (generation=%t model=%q choices=%d attempt=%d attempts=%s selected=%s)", result.Wire.ID != "", result.Wire.Model, len(result.Wire.Choices), result.Wire.Metadata.Attempt, structuredAttemptSummary(result.Wire), structuredEndpointSummary(result.Wire))
	}
	result.StructuredOutput = result.Wire.Choices[0].Message.Content
	return result, nil
}

func validOpenRouterVideoMIME(value string) bool {
	switch value {
	case "video/mp4", "video/mpeg", "video/mov", "video/webm":
		return true
	default:
		return false
	}
}

func validStructuredAttemptLedger(wire openRouterStructuredResponse, config openRouterStructuredCallConfig) bool {
	if len(wire.Metadata.Attempts) == 0 {
		return true
	}
	if len(wire.Metadata.Attempts) != 1 {
		return false
	}
	attempt := wire.Metadata.Attempts[0]
	return attempt.Provider == config.UpstreamProvider && attempt.Model == config.ResolvedModel && attempt.Status >= 200 && attempt.Status < 300
}

func selectedStructuredEndpoint(wire openRouterStructuredResponse, config openRouterStructuredCallConfig) bool {
	selected := 0
	for _, endpoint := range wire.Metadata.Endpoints.Available {
		if !endpoint.Selected {
			continue
		}
		selected++
		if endpoint.Provider != config.UpstreamProvider || endpoint.Model != config.ResolvedModel {
			return false
		}
	}
	return selected == 1
}

func structuredAttemptSummary(wire openRouterStructuredResponse) string {
	parts := make([]string, 0, len(wire.Metadata.Attempts))
	for _, attempt := range wire.Metadata.Attempts {
		parts = append(parts, fmt.Sprintf("%q/%q/%d", attempt.Provider, attempt.Model, attempt.Status))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func structuredEndpointSummary(wire openRouterStructuredResponse) string {
	parts := make([]string, 0, len(wire.Metadata.Endpoints.Available))
	for _, endpoint := range wire.Metadata.Endpoints.Available {
		if endpoint.Selected {
			parts = append(parts, fmt.Sprintf("%q/%q", endpoint.Provider, endpoint.Model))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Compatibility alias for package-local tests and checkpoint code while the
// review-specific caller migrates onto the shared transport module.
type openRouterReviewResponse = openRouterStructuredResponse
