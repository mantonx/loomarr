package openroutermedia

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

func TestRouteAuthorityRejectsInvalidSnapshotBeforeReservationOrHTTP(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*CapabilitySnapshot, *RouteRequirements, *string)
	}{
		{name: "stale", mutate: func(s *CapabilitySnapshot, _ *RouteRequirements, _ *string) {
			s.RetrievedAt = now.Add(-24*time.Hour - time.Nanosecond)
		}},
		{name: "future dated", mutate: func(s *CapabilitySnapshot, _ *RouteRequirements, _ *string) { s.RetrievedAt = now.Add(time.Nanosecond) }},
		{name: "digest mismatch", mutate: func(_ *CapabilitySnapshot, _ *RouteRequirements, digest *string) {
			*digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
		{name: "base URL mismatch", mutate: func(_ *CapabilitySnapshot, r *RouteRequirements, _ *string) { r.BaseURL = "https://other.test/api/v1" }},
		{name: "requested model mismatch", mutate: func(_ *CapabilitySnapshot, r *RouteRequirements, _ *string) { r.RequestedModel = "vendor/other" }},
		{name: "canonical model mismatch", mutate: func(_ *CapabilitySnapshot, r *RouteRequirements, _ *string) { r.CanonicalModel = "vendor/other-2026" }},
		{name: "upstream mismatch", mutate: func(_ *CapabilitySnapshot, r *RouteRequirements, _ *string) { r.UpstreamProvider = "Other Provider" }},
		{name: "provider route mismatch", mutate: func(_ *CapabilitySnapshot, r *RouteRequirements, _ *string) { r.ProviderSlug = "other/provider" }},
		{name: "audio absent", mutate: func(s *CapabilitySnapshot, _ *RouteRequirements, _ *string) {
			s.Models[0].InputModalities = []string{"image", "text", "video"}
		}},
		{name: "image absent", mutate: func(s *CapabilitySnapshot, _ *RouteRequirements, _ *string) {
			s.Models[0].InputModalities = []string{"audio", "text", "video"}
		}},
		{name: "video absent", mutate: func(s *CapabilitySnapshot, _ *RouteRequirements, _ *string) {
			s.Models[0].InputModalities = []string{"audio", "image", "text"}
		}},
		{name: "structured output absent", mutate: func(s *CapabilitySnapshot, _ *RouteRequirements, _ *string) {
			s.Models[0].Endpoints[0].SupportedParameters = []string{"response_format"}
		}},
		{name: "reasoning absent", mutate: func(_ *CapabilitySnapshot, r *RouteRequirements, _ *string) { r.RequireReasoning = true }},
		{name: "ZDR ineligible", mutate: func(s *CapabilitySnapshot, _ *RouteRequirements, _ *string) { s.Models[0].Endpoints[0].ZDR = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := authorityTestSnapshot(now)
			requirements := authorityTestRequirements(now)
			digest := ""
			test.mutate(&snapshot, &requirements, &digest)
			if digest == "" {
				digest = CapabilitySnapshotSHA256(snapshot)
			}
			assertAuthorityRejectedBeforeUse(t, snapshot, digest, requirements)
		})
	}
}

func TestRouteAuthorityRevalidatesFreshnessAndExactRouteBeforeUse(t *testing.T) {
	t.Parallel()
	issuedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	current := issuedAt
	snapshot := authorityTestSnapshot(issuedAt)
	requirements := authorityTestRequirements(issuedAt)
	requirements.Now = func() time.Time { return current }
	authority, err := NewRouteAuthority(snapshot, CapabilitySnapshotSHA256(snapshot), requirements)
	if err != nil {
		t.Fatal(err)
	}

	current = issuedAt.Add(24*time.Hour + time.Nanosecond)
	assertCallRejectedBeforeUse(t, authority, authorityTestConfig())
	current = issuedAt
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Model = "vendor/other" },
		func(c *Config) { c.ResolvedModel = "vendor/other-2026" },
		func(c *Config) { c.UpstreamProvider = "Other Provider" },
		func(c *Config) { c.ProviderSlug = "other/provider" },
		func(c *Config) { c.MaxTokens++ },
	} {
		config := authorityTestConfig()
		mutate(&config)
		assertCallRejectedBeforeUse(t, authority, config)
	}
	config := authorityTestConfig()
	config.Authority = authority
	reserved, requested := false, false
	config.Reserve = func(string) error { reserved = true; return nil }
	client := &http.Client{Transport: httpfixture.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		requested = true
		return nil, errors.New("unexpected HTTP")
	})}
	if _, err := Call(t.Context(), client, "https://other.test/api/v1", config); err == nil || reserved || requested {
		t.Fatalf("base mismatch: err=%v reserved=%t requested=%t", err, reserved, requested)
	}
	assertCallRejectedBeforeUse(t, RouteAuthority{}, authorityTestConfig())
}

func assertAuthorityRejectedBeforeUse(t *testing.T, snapshot CapabilitySnapshot, digest string, requirements RouteRequirements) {
	t.Helper()
	authority, err := NewRouteAuthority(snapshot, digest, requirements)
	if err == nil {
		t.Fatal("invalid evidence issued route authority")
	}
	assertCallRejectedBeforeUse(t, authority, authorityTestConfig())
}

func assertCallRejectedBeforeUse(t *testing.T, authority RouteAuthority, config Config) {
	t.Helper()
	reserved, requested := false, false
	config.Authority = authority
	config.Reserve = func(string) error { reserved = true; return nil }
	client := &http.Client{Transport: httpfixture.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		requested = true
		return nil, errors.New("unexpected HTTP")
	})}
	if _, err := Call(t.Context(), client, "https://openrouter.test/api/v1", config); err == nil || reserved || requested {
		t.Fatalf("err=%v reserved=%t requested=%t", err, reserved, requested)
	}
}

func authorityTestSnapshot(now time.Time) CapabilitySnapshot {
	return CapabilitySnapshot{SchemaVersion: CapabilitySnapshotSchemaVersion, SourceBaseURL: "https://openrouter.test/api/v1", RetrievedAt: now, Requests: 3, ResponseBytes: 1, Models: []CapabilityModelSnapshot{{ID: "vendor/model", CanonicalSlug: "vendor/model-2026", Name: "Model", Created: 1, InputModalities: []string{"audio", "image", "text", "video"}, OutputModalities: []string{"text"}, Endpoints: []CapabilityEndpointSnapshot{{Name: "Pinned", ModelID: "vendor/model", ProviderName: "Pinned Provider", ProviderSlug: "pinned/provider", ContextLength: 4096, MaxCompletionTokens: 4096, SupportedParameters: []string{"response_format", "structured_outputs"}, Pricing: map[string]string{"completion": "0.000001", "prompt": "0.000001"}, ZDR: true}}}}}
}

func authorityTestRequirements(now time.Time) RouteRequirements {
	return RouteRequirements{BaseURL: "https://openrouter.test/api/v1", RequestedModel: "vendor/model", CanonicalModel: "vendor/model-2026", UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider", RequiredInputModalities: []string{"audio", "image", "text", "video"}, MaxTokens: 32, Now: func() time.Time { return now }}
}

func authorityTestConfig() Config {
	return Config{APIKey: "secret", Model: "vendor/model", ResolvedModel: "vendor/model-2026", UpstreamProvider: "Pinned Provider", ProviderSlug: "pinned/provider", SchemaName: "contract", Schema: map[string]any{"type": "object"}, SystemPrompt: "system", Content: "content", MaxTokens: 32, MaxChargeNanoUSD: 1}
}
