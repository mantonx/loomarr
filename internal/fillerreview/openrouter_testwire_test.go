package fillerreview

// These provider-wire types belong only to caller tests that verify which
// prompts and media the shared transport received. Production callers consume
// the transport's stable result rather than its HTTP response shape.
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
