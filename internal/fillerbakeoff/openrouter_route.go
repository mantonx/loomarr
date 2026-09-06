package fillerbakeoff

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
)

// ValidateOpenRouterVideoRoute proves that one fresh exact route can accept the strict direct-video
// contract. The returned values are copies from the immutable snapshot.
func ValidateOpenRouterVideoRoute(snapshot OpenRouterSnapshot, modelID, upstreamProvider, upstreamProviderSlug string, at time.Time, maximumCompletionTokens int64) (OpenRouterModelSnapshot, OpenRouterEndpointSnapshot, error) {
	if err := ValidateOpenRouterSnapshot(snapshot); err != nil {
		return OpenRouterModelSnapshot{}, OpenRouterEndpointSnapshot{}, err
	}
	if snapshot.SourceBaseURL != OpenRouterBaseURL || at.IsZero() || at != at.UTC() {
		return OpenRouterModelSnapshot{}, OpenRouterEndpointSnapshot{}, fmt.Errorf("OpenRouter video route requires canonical metadata source and UTC validation time")
	}
	age := at.Sub(snapshot.RetrievedAt)
	if age < 0 || age > maxSnapshotAge {
		return OpenRouterModelSnapshot{}, OpenRouterEndpointSnapshot{}, fmt.Errorf("OpenRouter video route is outside the snapshot's 24-hour window")
	}
	model, ok := snapshotModel(snapshot, modelID)
	if !ok || !slices.Contains(model.InputModalities, "text") || !slices.Contains(model.InputModalities, "video") {
		return OpenRouterModelSnapshot{}, OpenRouterEndpointSnapshot{}, fmt.Errorf("OpenRouter video model is absent or lacks text/video input")
	}
	endpoint, ok := snapshotEndpoint(model, upstreamProviderSlug, upstreamProvider)
	if !ok || !endpoint.ZDR || endpoint.Status != 0 || maximumCompletionTokens <= 0 ||
		endpoint.MaxCompletionTokens < maximumCompletionTokens ||
		!slices.Contains(endpoint.SupportedParameters, "response_format") ||
		!slices.Contains(endpoint.SupportedParameters, "structured_outputs") ||
		!slices.Contains(endpoint.SupportedParameters, "reasoning") {
		return OpenRouterModelSnapshot{}, OpenRouterEndpointSnapshot{}, fmt.Errorf("OpenRouter video route is absent, non-ZDR, inactive, or lacks strict output and reasoning control")
	}
	return model, endpoint, nil
}

// EstimateOpenRouterTokenChargeNanoUSD returns the greatest possible charge up
// to the supplied token ceilings, using exact decimal snapshot prices across
// every applicable prompt-token pricing tier.
func EstimateOpenRouterTokenChargeNanoUSD(endpoint OpenRouterEndpointSnapshot, maximumInputTokens, maximumCompletionTokens int64) (int64, error) {
	if maximumInputTokens <= 0 || maximumCompletionTokens <= 0 || endpoint.ContextLength <= 0 ||
		maximumInputTokens > endpoint.ContextLength-maximumCompletionTokens {
		return 0, fmt.Errorf("OpenRouter maximum input-token allowance is invalid for the route context")
	}
	if endpoint.MaxPromptTokens > 0 && maximumInputTokens > endpoint.MaxPromptTokens {
		return 0, fmt.Errorf("OpenRouter maximum input-token allowance exceeds the route limit")
	}
	previousThreshold := int64(0)
	candidates := []int64{maximumInputTokens}
	for _, override := range endpoint.PricingOverrides {
		if override.MinimumPromptTokens <= previousThreshold {
			return 0, fmt.Errorf("OpenRouter route has non-canonical pricing overrides")
		}
		previousThreshold = override.MinimumPromptTokens
		if override.MinimumPromptTokens > 1 && override.MinimumPromptTokens <= maximumInputTokens {
			candidates = append(candidates, override.MinimumPromptTokens-1)
		}
	}
	maximumCharge := int64(0)
	for _, inputTokens := range candidates {
		pricing := endpoint.Pricing
		for _, override := range endpoint.PricingOverrides {
			if override.MinimumPromptTokens > inputTokens {
				break
			}
			pricing = overlayOpenRouterPricing(pricing, override.Pricing)
		}
		charge, err := openRouterTokenChargeNanoUSD(pricing, inputTokens, maximumCompletionTokens)
		if err != nil {
			return 0, err
		}
		if charge > maximumCharge {
			maximumCharge = charge
		}
	}
	return maximumCharge, nil
}

func overlayOpenRouterPricing(base, override map[string]string) map[string]string {
	pricing := make(map[string]string, len(base)+len(override))
	for name, price := range base {
		pricing[name] = price
	}
	for name, price := range override {
		pricing[name] = price
	}
	return pricing
}

func openRouterTokenChargeNanoUSD(pricing map[string]string, inputTokens, completionTokens int64) (int64, error) {
	prompt, ok := new(big.Rat).SetString(strings.TrimSpace(pricing["prompt"]))
	if !ok || prompt.Sign() < 0 {
		return 0, fmt.Errorf("OpenRouter route has invalid prompt pricing")
	}
	completion, ok := new(big.Rat).SetString(strings.TrimSpace(pricing["completion"]))
	if !ok || completion.Sign() < 0 {
		return 0, fmt.Errorf("OpenRouter route has invalid completion pricing")
	}
	total := new(big.Rat).Mul(prompt, big.NewRat(inputTokens, 1))
	total.Add(total, new(big.Rat).Mul(completion, big.NewRat(completionTokens, 1)))
	total.Mul(total, big.NewRat(1_000_000_000, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(total.Num(), total.Denom(), remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("OpenRouter route price bound exceeds nanodollar range")
	}
	return quotient.Int64(), nil
}
