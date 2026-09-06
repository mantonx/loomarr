package fillersafety

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenRouterRuntimeProfileDerivesStableCertificationIdentity(t *testing.T) {
	policy := validPolicyFixture()
	first, err := OpenRouterRuntimeProfile(policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenRouterRuntimeProfile(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.PolicySHA256 != policySHA256(policy) ||
		first.ProposerSHA256 == "" || first.EvaluationImplementation != evaluationImplementation ||
		first.Audio.PromptSHA256 != audioPromptSHA256(policy) || first.Audio.SchemaSHA256 != audioSchemaSHA256(policy) ||
		first.Video.PromptSHA256 != videoPromptSHA256() || first.Video.SchemaSHA256 != videoSchemaContractSHA256() ||
		!reflect.DeepEqual(first.Audio.Modalities, []string{"audio"}) ||
		!reflect.DeepEqual(first.Video.Modalities, []string{"audio", "video"}) {
		t.Fatalf("runtime profile = %+v", first)
	}

	changed := policy
	changed.Rules = append([]PolicyRule(nil), policy.Rules...)
	changed.Rules[0].Variants = []string{"different private value"}
	other, err := OpenRouterRuntimeProfile(changed)
	if err != nil {
		t.Fatal(err)
	}
	if other.PolicySHA256 == first.PolicySHA256 || other.Audio.PromptSHA256 != first.Audio.PromptSHA256 ||
		other.Audio.SchemaSHA256 != first.Audio.SchemaSHA256 {
		t.Fatalf("policy/profile drift was not represented correctly: first=%+v other=%+v", first, other)
	}
}

func TestNewOpenRouterEvaluationOperationOwnsConcreteCascade(t *testing.T) {
	config := validOpenRouterEvaluationConfig()
	operation, profile, err := NewOpenRouterEvaluationOperation(config)
	if err != nil {
		t.Fatal(err)
	}
	if operation == nil || profile.PolicySHA256 != policySHA256(config.Policy) {
		t.Fatalf("operation=%v profile=%+v", operation, profile)
	}

	invalid := config
	invalid.Audio.CapabilitySHA256 = ""
	if operation, _, err := NewOpenRouterEvaluationOperation(invalid); err == nil || operation != nil {
		t.Fatal("constructor accepted an incomplete hosted route")
	}
	invalid = config
	invalid.Budget.PerRunNanoUSD = -1
	if operation, _, err := NewOpenRouterEvaluationOperation(invalid); err == nil || operation != nil {
		t.Fatal("constructor accepted a negative durable budget")
	}
}

func TestVideoSchemaCertificationIdentityIsStableAcrossRuntimeDurations(t *testing.T) {
	config := validOpenRouterEvaluationConfig()
	_, profile, err := NewOpenRouterEvaluationOperation(config)
	if err != nil {
		t.Fatal(err)
	}
	corroborator := &openRouterVideoCorroborator{config: openRouterVideoConfig{
		PromptSHA256: profile.Video.PromptSHA256, CapabilitySHA256: config.Video.CapabilitySHA256,
		Model: config.Video.Model, ResolvedModel: config.Video.ResolvedModel,
		UpstreamProvider: config.Video.UpstreamProvider,
	}}
	short := corroborator.identity(1_000)
	long := corroborator.identity(120_000)
	if short.SchemaSHA256 != long.SchemaSHA256 || short.SchemaSHA256 != profile.Video.SchemaSHA256 {
		t.Fatalf("schema contract drifted with duration: short=%s long=%s profile=%s", short.SchemaSHA256, long.SchemaSHA256, profile.Video.SchemaSHA256)
	}
	if canonicalJSONSHA256(videoOutputSchema(1_000)) == canonicalJSONSHA256(videoOutputSchema(120_000)) {
		t.Fatal("request-specific schemas unexpectedly lost their duration bounds")
	}
}

func validOpenRouterEvaluationConfig() OpenRouterEvaluationConfig {
	route := OpenRouterRouteConfig{
		Model: "vendor/model", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider",
		CapabilitySHA256: strings.Repeat("a", 64), MaxChargeNanoUSD: 1_000,
		DisableReasoning: true,
	}
	return OpenRouterEvaluationConfig{
		Repository: recordedRepository(&operationRepositoryState{}), Policy: validPolicyFixture(), FFmpegPath: "ffmpeg",
		Client: http.DefaultClient, BaseURL: "https://openrouter.example/api/v1", APIKey: "private-key",
		Audio: route, Video: route,
		Budget: HostedCallBudget{PerClipNanoUSD: 10_000, PerDayNanoUSD: 100_000, PerRunNanoUSD: 10_000},
		Now:    time.Now,
	}
}
