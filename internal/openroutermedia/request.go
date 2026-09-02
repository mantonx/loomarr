package openroutermedia

import (
	"encoding/json"
	"fmt"
	"strings"
)

type structuredRequest struct {
	Model          string               `json:"model"`
	Messages       []structuredMessage  `json:"messages"`
	Provider       structuredRoute      `json:"provider"`
	ResponseFormat structuredFormat     `json:"response_format"`
	Reasoning      *structuredReasoning `json:"reasoning,omitempty"`
	MaxTokens      int                  `json:"max_tokens"`
}

type structuredReasoning struct {
	Enabled bool `json:"enabled"`
}

type structuredMessage struct {
	Role    string           `json:"role"`
	Content []structuredPart `json:"content"`
}

type structuredPart struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	ImageURL *structuredMediaURL `json:"image_url,omitempty"`
	VideoURL *structuredMediaURL `json:"video_url,omitempty"`
}

type structuredMediaURL struct {
	URL string `json:"url"`
}

type structuredRoute struct {
	Order             []string `json:"order"`
	AllowFallbacks    bool     `json:"allow_fallbacks"`
	RequireParameters bool     `json:"require_parameters"`
	DataCollection    string   `json:"data_collection"`
	ZDR               bool     `json:"zdr"`
}

type structuredFormat struct {
	Type       string               `json:"type"`
	JSONSchema structuredJSONSchema `json:"json_schema"`
}

type structuredJSONSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

func buildRequest(config Config) ([]byte, error) {
	parts := []structuredPart{{Type: "text", Text: config.Content}}
	for _, image := range config.Images {
		parts = append(parts, structuredPart{Type: "image_url", ImageURL: &structuredMediaURL{URL: "data:image/jpeg;base64," + image}})
	}
	for _, video := range config.Videos {
		if !validVideoMIME(video.MIMEType) || strings.TrimSpace(video.Base64) == "" {
			return nil, fmt.Errorf("OpenRouter structured video input has an invalid MIME type or empty payload")
		}
		parts = append(parts, structuredPart{Type: "video_url", VideoURL: &structuredMediaURL{URL: "data:" + video.MIMEType + ";base64," + video.Base64}})
	}
	payload := structuredRequest{
		Model: config.Model,
		Messages: []structuredMessage{
			{Role: "system", Content: []structuredPart{{Type: "text", Text: config.SystemPrompt}}},
			{Role: "user", Content: parts},
		},
		Provider:       structuredRoute{Order: []string{config.ProviderSlug}, RequireParameters: true, DataCollection: "deny", ZDR: true},
		ResponseFormat: structuredFormat{Type: "json_schema", JSONSchema: structuredJSONSchema{Name: config.SchemaName, Strict: true, Schema: config.Schema}},
		MaxTokens:      config.MaxTokens,
	}
	if config.DisableReasoning {
		payload.Reasoning = &structuredReasoning{Enabled: false}
	}
	return json.Marshal(payload)
}

func validVideoMIME(value string) bool {
	switch value {
	case "video/mp4", "video/mpeg", "video/mov", "video/webm":
		return true
	default:
		return false
	}
}
