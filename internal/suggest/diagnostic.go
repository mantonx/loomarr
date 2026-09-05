package suggest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
)

const (
	PlannerPromptVersion             = "suggester-prompt-v4"
	PlannerToolSchemaVersion         = "catalog-search-v4"
	PlannerMessageTemplateVersion    = "planner-tool-result-finalization-v1"
	PlannerDiagnosticToolCallID      = "planner-diagnostic-call-1"
	plannerDiagnosticSchemaVersion   = 1
	plannerDiagnosticIntent          = "science fiction"
	plannerDiagnosticResponseVersion = "single-grounded-candidate-v1"
)

// ToolFinalizationDiagnostic is a content-safe identity and outcome record for
// the exact production turn immediately after a non-empty catalog result. It
// intentionally records hashes rather than prompts, tool results, model output,
// credentials, or reasoning content.
type ToolFinalizationDiagnostic struct {
	SchemaVersion          int      `json:"schemaVersion"`
	PromptVersion          string   `json:"promptVersion"`
	ToolSchemaVersion      string   `json:"toolSchemaVersion"`
	MessageTemplateVersion string   `json:"messageTemplateVersion"`
	SyntheticResultVersion string   `json:"syntheticResultVersion"`
	SystemPromptSHA256     string   `json:"systemPromptSha256"`
	UserPromptSHA256       string   `json:"userPromptSha256"`
	MessagesSHA256         string   `json:"messagesSha256"`
	ToolSchemaSHA256       string   `json:"toolSchemaSha256"`
	MessageRoles           []string `json:"messageRoles"`
	ToolCallID             string   `json:"toolCallId"`
	JSONMode               bool     `json:"jsonMode"`
	ToolsOff               bool     `json:"toolsOff"`
	Temperature            float64  `json:"temperature"`
	MaxTokens              int      `json:"maxTokens"`
	ProviderAdapter        string   `json:"providerAdapter"`
	ThinkingSetting        string   `json:"thinkingSetting"`
	RequestedProvider      string   `json:"requestedProvider"`
	RequestedModel         string   `json:"requestedModel"`
	ResolvedProvider       string   `json:"resolvedProvider,omitempty"`
	ResolvedModel          string   `json:"resolvedModel,omitempty"`
	JSONValid              bool     `json:"jsonValid"`
	RepeatedToolCall       bool     `json:"repeatedToolCall"`
	ResponseContentSHA256  string   `json:"responseContentSha256"`
	AttributionAttempts    int      `json:"attributionAttempts,omitempty"`
	ChargeAmount           string   `json:"chargeAmount,omitempty"`
	ChargeCurrency         string   `json:"chargeCurrency,omitempty"`
}

// RunToolFinalizationDiagnostic executes one model turn using the same rendered
// system/user messages, tool schema, assistant tool-call correlation, tool-result
// shape, sampling controls, and post-result JSON mode as Suggester. The synthetic
// result is fixed and contains no user or installation data.
func RunToolFinalizationDiagnostic(ctx context.Context, provider llm.Provider, model string) (ToolFinalizationDiagnostic, error) {
	toolCall := llm.ToolCall{
		ID: PlannerDiagnosticToolCallID, Name: catalogToolName,
		Arguments: map[string]any{"genres": []any{"Science Fiction"}, "media_type": "movie"},
	}
	candidates := []catalog.Candidate{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix", Year: 1999,
		InLibrary: true, Genres: []string{"Action", "Science Fiction"},
		Overview: "A grounded synthetic catalog candidate for adapter diagnostics.",
	}}
	result, err := json.Marshal(toolResult(candidates))
	if err != nil {
		return ToolFinalizationDiagnostic{}, fmt.Errorf("marshal diagnostic tool result: %w", err)
	}
	messages := []llm.Message{
		{Role: llm.System, Content: systemPrompt},
		{Role: llm.User, Content: userPrompt(Intent{Description: plannerDiagnosticIntent})},
		assistantToolCallMsg([]llm.ToolCall{toolCall}),
		{Role: llm.Tool, Content: string(result), ToolCallID: PlannerDiagnosticToolCallID},
	}
	tools := []llm.ToolSchema{catalogTool()}
	messageBlob, err := json.Marshal(messages)
	if err != nil {
		return ToolFinalizationDiagnostic{}, fmt.Errorf("marshal diagnostic messages: %w", err)
	}
	toolBlob, err := json.Marshal(tools)
	if err != nil {
		return ToolFinalizationDiagnostic{}, fmt.Errorf("marshal diagnostic tool schema: %w", err)
	}
	opts := chatOpts(nil, groundedTemp)
	report := ToolFinalizationDiagnostic{
		SchemaVersion: plannerDiagnosticSchemaVersion,
		PromptVersion: PlannerPromptVersion, ToolSchemaVersion: PlannerToolSchemaVersion,
		MessageTemplateVersion: PlannerMessageTemplateVersion,
		SyntheticResultVersion: plannerDiagnosticResponseVersion,
		SystemPromptSHA256:     sha256Hex([]byte(messages[0].Content)),
		UserPromptSHA256:       sha256Hex([]byte(messages[1].Content)),
		MessagesSHA256:         sha256Hex(messageBlob), ToolSchemaSHA256: sha256Hex(toolBlob),
		MessageRoles: []string{string(llm.System), string(llm.User), string(llm.Assistant), string(llm.Tool)},
		ToolCallID:   PlannerDiagnosticToolCallID,
		JSONMode:     opts.JSONMode, ToolsOff: len(opts.Tools) == 0,
		Temperature: groundedTemp, MaxTokens: opts.MaxTokens,
		ProviderAdapter: adapterIdentity(provider.Name()), ThinkingSetting: thinkingSetting(provider.Name()),
		RequestedProvider: provider.Name(), RequestedModel: model,
	}
	response, err := provider.Chat(ctx, messages, opts)
	if err != nil {
		return report, fmt.Errorf("post-result diagnostic chat: %w", err)
	}
	report.ResolvedProvider = response.Attribution.ResolvedProvider
	report.ResolvedModel = response.Attribution.ResolvedModel
	report.AttributionAttempts = response.Attribution.Attempts
	if response.Attribution.Charge != nil {
		report.ChargeAmount = response.Attribution.Charge.Amount
		report.ChargeCurrency = response.Attribution.Charge.Currency
	}
	report.JSONValid = json.Valid([]byte(response.Content))
	report.RepeatedToolCall = response.WantsTools()
	report.ResponseContentSHA256 = sha256Hex([]byte(response.Content))
	return report, nil
}

func sha256Hex(blob []byte) string {
	digest := sha256.Sum256(blob)
	return hex.EncodeToString(digest[:])
}

func adapterIdentity(provider string) string {
	if provider == "ollama" {
		return "ollama-native-chat-v1"
	}
	return "openai-compatible-chat-completions-v1"
}

func thinkingSetting(provider string) string {
	if provider == "ollama" {
		return "think-false-on-json-v1"
	}
	return "provider-default-unset-v1"
}
