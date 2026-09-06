package app

import "testing"

func TestOpenRouterStructureAPIKeyUsesNamespacedProviderSecret(t *testing.T) {
	set := visionSet(t, map[string]string{
		"llm.provider":           "ollama",
		"llm.api_key":            "unrelated-base-key",
		"llm.api_key.openrouter": "structure-key",
	})
	if got := openRouterStructureAPIKey(set); got != "structure-key" {
		t.Fatalf("key=%q", got)
	}
}

func TestOpenRouterStructureAPIKeyDoesNotReuseAnotherProviderSecret(t *testing.T) {
	set := visionSet(t, map[string]string{
		"llm.provider": "openai",
		"llm.api_key":  "other-provider-key",
	})
	if got := openRouterStructureAPIKey(set); got != "" {
		t.Fatalf("key=%q", got)
	}
}
