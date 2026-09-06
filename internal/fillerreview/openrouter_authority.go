package fillerreview

import (
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

func openRouterRouteAuthority(snapshot fillerbakeoff.OpenRouterSnapshot, expectedSHA256, baseURL, model, canonicalModel, provider, providerSlug string, modalities []string, maxTokens int, requireReasoning bool, now func() time.Time) (openroutermedia.RouteAuthority, error) {
	return openroutermedia.NewRouteAuthority(snapshot, expectedSHA256, openroutermedia.RouteRequirements{
		BaseURL: baseURL, RequestedModel: model, CanonicalModel: canonicalModel,
		UpstreamProvider: provider, ProviderSlug: providerSlug,
		RequiredInputModalities: modalities, MaxTokens: maxTokens,
		RequireReasoning: requireReasoning, Now: now,
	})
}
