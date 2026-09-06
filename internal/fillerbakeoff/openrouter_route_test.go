package fillerbakeoff

import (
	"strings"
	"testing"
	"time"
)

func TestValidateOpenRouterVideoRouteAndExactPriceBound(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	snapshot.Models[0].InputModalities = []string{"text", "video"}
	snapshot.Models[0].Endpoints[0].SupportedParameters = []string{"reasoning", "response_format", "structured_outputs"}
	model, endpoint, err := ValidateOpenRouterVideoRoute(
		snapshot, "vendor/model-1", "Pinned Provider", "pinned-provider/variant",
		snapshot.RetrievedAt.Add(time.Hour), 1024,
	)
	if err != nil {
		t.Fatal(err)
	}
	if model.CanonicalSlug != "vendor/model-1-20260826" || endpoint.ProviderSlug != "pinned-provider/variant" {
		t.Fatalf("model=%+v endpoint=%+v", model, endpoint)
	}
	charge, err := EstimateOpenRouterTokenChargeNanoUSD(endpoint, 1_000, 1_024)
	if err != nil || charge != 3_048_000 {
		t.Fatalf("charge=%d error=%v", charge, err)
	}

	stale := snapshot
	if _, _, err := ValidateOpenRouterVideoRoute(stale, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", stale.RetrievedAt.Add(25*time.Hour), 1024); err == nil || !strings.Contains(err.Error(), "24-hour") {
		t.Fatalf("stale error=%v", err)
	}
	private := snapshot
	private.Models = append([]OpenRouterModelSnapshot(nil), snapshot.Models...)
	private.Models[0].Endpoints = append([]OpenRouterEndpointSnapshot(nil), snapshot.Models[0].Endpoints...)
	private.Models[0].Endpoints[0].ZDR = false
	if _, _, err := ValidateOpenRouterVideoRoute(private, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", private.RetrievedAt, 1024); err == nil || !strings.Contains(err.Error(), "non-ZDR") {
		t.Fatalf("privacy error=%v", err)
	}
}

func TestEstimateOpenRouterTokenChargeNanoUSDHonorsPricingTiers(t *testing.T) {
	t.Parallel()
	endpoint := validOpenRouterSnapshot().Models[0].Endpoints[0]
	endpoint.ContextLength = 500_000
	endpoint.MaxPromptTokens = 400_000
	endpoint.PricingOverrides = []OpenRouterPricingOverride{{
		MinimumPromptTokens: 200_000,
		Pricing:             map[string]string{"prompt": "0.000002", "completion": "0.000009"},
	}}

	below, err := EstimateOpenRouterTokenChargeNanoUSD(endpoint, 100_000, 1_000)
	if err != nil || below != 102_000_000 {
		t.Fatalf("below-tier charge=%d error=%v", below, err)
	}
	above, err := EstimateOpenRouterTokenChargeNanoUSD(endpoint, 300_000, 1_000)
	if err != nil || above != 609_000_000 {
		t.Fatalf("above-tier charge=%d error=%v", above, err)
	}
}

func TestEstimateOpenRouterTokenChargeNanoUSDKeepsWorstEarlierTier(t *testing.T) {
	t.Parallel()
	endpoint := validOpenRouterSnapshot().Models[0].Endpoints[0]
	endpoint.ContextLength = 500_000
	endpoint.MaxPromptTokens = 400_000
	endpoint.Pricing = map[string]string{"prompt": "0.000010", "completion": "0"}
	endpoint.PricingOverrides = []OpenRouterPricingOverride{{
		MinimumPromptTokens: 200_000,
		Pricing:             map[string]string{"prompt": "0.000001"},
	}}

	charge, err := EstimateOpenRouterTokenChargeNanoUSD(endpoint, 300_000, 1_000)
	if err != nil || charge != 1_999_990_000 {
		t.Fatalf("worst-tier charge=%d error=%v", charge, err)
	}
}

func TestValidateOpenRouterSnapshotRejectsNonCanonicalPricingTiers(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	snapshot.Models[0].Endpoints[0].PricingOverrides = []OpenRouterPricingOverride{
		{MinimumPromptTokens: 200_000, Pricing: map[string]string{"prompt": "0.000002"}},
		{MinimumPromptTokens: 200_000, Pricing: map[string]string{"prompt": "0.000003"}},
	}
	if err := ValidateOpenRouterSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("tier validation error=%v", err)
	}
}
