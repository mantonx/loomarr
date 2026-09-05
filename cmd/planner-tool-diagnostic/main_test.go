package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/suggest"
)

func TestRunRequiresExplicitEndpointAndModel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), func(string) string { return "" }, &stdout, &stderr, nil)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "LLM_URL and LLM_MODEL") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunEmitsContentSafeJSONReport(t *testing.T) {
	env := map[string]string{
		"LLM_PROVIDER": "ollama", "LLM_URL": "http://127.0.0.1:11434", "LLM_MODEL": "fixture",
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), func(key string) string { return env[key] }, &stdout, &stderr,
		func(_ context.Context, provider llm.Provider, model string) (suggest.ToolFinalizationDiagnostic, error) {
			if provider.Name() != "ollama" || model != "fixture" {
				t.Fatalf("provider/model = %q/%q", provider.Name(), model)
			}
			return suggest.ToolFinalizationDiagnostic{SchemaVersion: 1, PromptVersion: suggest.PlannerPromptVersion}, nil
		})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"promptVersion": "suggester-prompt-v4"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDiagnosticProviderRequiresPinnedOpenRouterRoute(t *testing.T) {
	_, err := diagnosticProvider("openrouter", "https://openrouter.ai/api/v1", "openai/gpt-oss-20b", "secret", "")
	if err == nil || !strings.Contains(err.Error(), "upstream provider") {
		t.Fatalf("route error = %v", err)
	}
}
