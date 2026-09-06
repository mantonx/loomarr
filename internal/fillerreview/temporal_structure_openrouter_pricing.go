package fillerreview

import (
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

func estimateTemporalStructureOpenRouterCharge(config TemporalStructureOpenRouterConfig) (int64, error) {
	endpoint, ok := temporalStructureOpenRouterEndpoint(config)
	if !ok {
		return 0, fmt.Errorf("OpenRouter structure route is absent from the snapshot")
	}
	return fillerbakeoff.EstimateOpenRouterTokenChargeNanoUSD(
		endpoint,
		config.MaximumInputTokens,
		int64(temporalStructureOpenRouterMaxTokens),
	)
}

func temporalStructureOpenRouterEndpoint(config TemporalStructureOpenRouterConfig) (fillerbakeoff.OpenRouterEndpointSnapshot, bool) {
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	for _, endpoint := range model.Endpoints {
		if endpoint.ProviderName == config.UpstreamProvider && endpoint.ProviderSlug == config.UpstreamProviderSlug {
			return endpoint, true
		}
	}
	return fillerbakeoff.OpenRouterEndpointSnapshot{}, false
}
