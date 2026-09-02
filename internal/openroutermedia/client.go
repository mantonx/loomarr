// Package openroutermedia owns Loomarr's bounded OpenRouter structured-media
// transport. Callers own prompts, output schemas, semantic decoding, and the
// durable ledger; this package owns the exact request, one-attempt route
// binding, response authority, and charge settlement.
package openroutermedia

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
)

const maxResponseBytes = 256 << 10

// Config describes one already-authorized structured-media request.
type Config struct {
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
	Videos           []Video
	MaxTokens        int
	MaxChargeNanoUSD int64
	DisableReasoning bool
	Title            string
	Reserve          func(string) error
}

// Video is one base64-encoded video payload with an explicit supported MIME type.
type Video struct {
	MIMEType string
	Base64   string
}

// Result retains the exact request, response, route, usage, and settlement
// authority needed by the caller's durable ledger.
type Result struct {
	GenerationID     string
	RawResponse      []byte
	RequestSHA256    string
	ResponseSHA256   string
	PromptTokens     int64
	CompletionTokens int64
	ChargedAmountUSD string
	ChargedNanoUSD   int64
	ChargeKnown      bool
	StructuredOutput string
	ReasoningBytes   int
}

// Call performs one fallback-disabled request after the caller durably
// reserves the exact request hash.
func Call(ctx context.Context, client *http.Client, baseURL string, config Config) (Result, error) {
	body, err := buildRequest(config)
	if err != nil {
		return Result{}, err
	}
	result := Result{RequestSHA256: hashBytes(body)}
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
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return result, fmt.Errorf("OpenRouter structured response exceeded its byte ceiling")
	}
	result.ResponseSHA256 = hashBytes(raw)
	result.RawResponse = bytes.Clone(raw)
	if response.StatusCode != http.StatusOK {
		return result, &StatusError{StatusCode: response.StatusCode, Detail: boundedMessage(raw)}
	}
	return settleResponse(result, raw, config)
}
