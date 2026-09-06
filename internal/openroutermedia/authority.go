package openroutermedia

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	CapabilitySnapshotSchemaVersion = 2
	maxCapabilityModels             = 16
	maxCapabilityEndpoints          = 256
	maxCapabilityResponseBytes      = 32 << 20
	maxCapabilityFieldBytes         = 512
	maxCapabilityAge                = 24 * time.Hour
)

// CapabilitySnapshot is the immutable OpenRouter model, endpoint, pricing,
// and privacy evidence from which a RouteAuthority is issued.
type CapabilitySnapshot struct {
	SchemaVersion int                       `json:"schemaVersion"`
	SourceBaseURL string                    `json:"sourceBaseUrl"`
	RetrievedAt   time.Time                 `json:"retrievedAt"`
	Requests      int                       `json:"requests"`
	ResponseBytes int64                     `json:"responseBytes"`
	Models        []CapabilityModelSnapshot `json:"models"`
}

type CapabilityModelSnapshot struct {
	ID               string                       `json:"id"`
	CanonicalSlug    string                       `json:"canonicalSlug"`
	Name             string                       `json:"name"`
	Created          int64                        `json:"created"`
	InputModalities  []string                     `json:"inputModalities"`
	OutputModalities []string                     `json:"outputModalities"`
	Endpoints        []CapabilityEndpointSnapshot `json:"endpoints"`
}

type CapabilityEndpointSnapshot struct {
	Name                  string            `json:"name"`
	ModelID               string            `json:"modelId"`
	ProviderName          string            `json:"providerName"`
	ProviderSlug          string            `json:"providerSlug"`
	Quantization          string            `json:"quantization"`
	ContextLength         int64             `json:"contextLength"`
	MaxCompletionTokens   int64             `json:"maxCompletionTokens,omitempty"`
	MaxPromptTokens       int64             `json:"maxPromptTokens,omitempty"`
	SupportedParameters   []string          `json:"supportedParameters"`
	Pricing               map[string]string `json:"pricing"`
	Status                int               `json:"status"`
	ZDR                   bool              `json:"zdr"`
	SupportsImplicitCache bool              `json:"supportsImplicitCaching"`
}

// RouteRequirements identifies the exact route and request capabilities that
// must be present in a fresh snapshot before one authority can be issued.
type RouteRequirements struct {
	BaseURL                 string
	RequestedModel          string
	CanonicalModel          string
	UpstreamProvider        string
	ProviderSlug            string
	RequiredInputModalities []string
	MaxTokens               int
	RequireReasoning        bool
	Now                     func() time.Time
}

type routeAuthoritySeal struct{}

var validRouteAuthoritySeal = &routeAuthoritySeal{}

// RouteAuthority is an opaque, non-forgeable proof that a fresh capability
// snapshot authorizes one exact OpenRouter route. Its zero value is invalid.
type RouteAuthority struct {
	seal             *routeAuthoritySeal
	snapshotSHA256   string
	baseURL          string
	requestedModel   string
	canonicalModel   string
	upstreamProvider string
	providerSlug     string
	inputModalities  []string
	maxTokens        int
	retrievedAt      time.Time
	now              func() time.Time
}

// NewRouteAuthority validates the complete snapshot and binds its digest,
// freshness, model identities, provider route, modalities, and eligibility.
func NewRouteAuthority(snapshot CapabilitySnapshot, expectedSHA256 string, requirements RouteRequirements) (RouteAuthority, error) {
	if err := ValidateCapabilitySnapshot(snapshot); err != nil {
		return RouteAuthority{}, err
	}
	digest := CapabilitySnapshotSHA256(snapshot)
	if !validSHA256(expectedSHA256) || expectedSHA256 != digest {
		return RouteAuthority{}, fmt.Errorf("OpenRouter capability snapshot digest does not match expected authority")
	}
	now := requirements.Now
	if now == nil {
		now = time.Now
	}
	authority := RouteAuthority{
		seal: validRouteAuthoritySeal, snapshotSHA256: digest,
		baseURL:        strings.TrimRight(strings.TrimSpace(requirements.BaseURL), "/"),
		requestedModel: requirements.RequestedModel, canonicalModel: requirements.CanonicalModel,
		upstreamProvider: requirements.UpstreamProvider, providerSlug: requirements.ProviderSlug,
		inputModalities: slices.Clone(requirements.RequiredInputModalities), maxTokens: requirements.MaxTokens,
		retrievedAt: snapshot.RetrievedAt, now: now,
	}
	if authority.baseURL == "" || authority.baseURL != snapshot.SourceBaseURL || authority.requestedModel == "" || authority.canonicalModel == "" || authority.upstreamProvider == "" || authority.providerSlug == "" || authority.maxTokens <= 0 || !canonicalStrings(authority.inputModalities) {
		return RouteAuthority{}, fmt.Errorf("OpenRouter route authority requirements are invalid")
	}
	var selectedModel *CapabilityModelSnapshot
	for i := range snapshot.Models {
		if snapshot.Models[i].ID == authority.requestedModel {
			selectedModel = &snapshot.Models[i]
			break
		}
	}
	if selectedModel == nil || selectedModel.CanonicalSlug != authority.canonicalModel {
		return RouteAuthority{}, fmt.Errorf("OpenRouter capability snapshot does not bind the requested and canonical model")
	}
	for _, modality := range authority.inputModalities {
		if !slices.Contains(selectedModel.InputModalities, modality) {
			return RouteAuthority{}, fmt.Errorf("OpenRouter capability snapshot lacks required %q input", modality)
		}
	}
	for _, endpoint := range selectedModel.Endpoints {
		if endpoint.ProviderName != authority.upstreamProvider || endpoint.ProviderSlug != authority.providerSlug {
			continue
		}
		eligible := endpoint.ZDR && endpoint.Status == 0 && endpoint.MaxCompletionTokens >= int64(authority.maxTokens) && slices.Contains(endpoint.SupportedParameters, "response_format") && slices.Contains(endpoint.SupportedParameters, "structured_outputs")
		if requirements.RequireReasoning {
			eligible = eligible && slices.Contains(endpoint.SupportedParameters, "reasoning")
		}
		if !eligible {
			return RouteAuthority{}, fmt.Errorf("OpenRouter capability snapshot route is not ZDR and structured-output eligible")
		}
		if endpoint.Pricing["prompt"] == "" || endpoint.Pricing["completion"] == "" {
			return RouteAuthority{}, fmt.Errorf("OpenRouter capability snapshot route lacks required pricing")
		}
		if err := authority.validateFreshness(); err != nil {
			return RouteAuthority{}, err
		}
		return authority, nil
	}
	return RouteAuthority{}, fmt.Errorf("OpenRouter capability snapshot does not contain the required provider route")
}

func (authority RouteAuthority) validateCall(baseURL string, config Config) error {
	if authority.seal != validRouteAuthoritySeal || authority.now == nil {
		return fmt.Errorf("OpenRouter structured call requires validated route authority")
	}
	if err := authority.validateFreshness(); err != nil {
		return err
	}
	if strings.TrimRight(strings.TrimSpace(baseURL), "/") != authority.baseURL || config.Model != authority.requestedModel || config.ResolvedModel != authority.canonicalModel || config.UpstreamProvider != authority.upstreamProvider || config.ProviderSlug != authority.providerSlug || config.MaxTokens > authority.maxTokens {
		return fmt.Errorf("OpenRouter structured call does not match validated route authority")
	}
	for _, modality := range requestInputModalities(config) {
		if !slices.Contains(authority.inputModalities, modality) {
			return fmt.Errorf("OpenRouter structured call uses unauthorized %q input", modality)
		}
	}
	return nil
}

func (authority RouteAuthority) validateFreshness() error {
	age := authority.now().UTC().Sub(authority.retrievedAt)
	if age < 0 || age > maxCapabilityAge {
		return fmt.Errorf("OpenRouter route authority is outside the snapshot's 24-hour window")
	}
	return nil
}

func requestInputModalities(config Config) []string {
	modalities := []string{"text"}
	if len(config.Images) > 0 {
		modalities = append(modalities, "image")
	}
	if len(config.Audios) > 0 {
		modalities = append(modalities, "audio")
	}
	if len(config.Videos) > 0 {
		modalities = append(modalities, "video")
	}
	return modalities
}

func CapabilitySnapshotSHA256(snapshot CapabilitySnapshot) string {
	data, err := json.Marshal(snapshot)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ValidateCapabilitySnapshot(snapshot CapabilitySnapshot) error {
	parsedSource, sourceErr := url.Parse(snapshot.SourceBaseURL)
	if snapshot.SchemaVersion != CapabilitySnapshotSchemaVersion || sourceErr != nil || parsedSource.Host == "" || snapshot.RetrievedAt.IsZero() || snapshot.RetrievedAt.Location() != time.UTC {
		return fmt.Errorf("OpenRouter snapshot requires schema %d and a UTC retrieval time", CapabilitySnapshotSchemaVersion)
	}
	if len(snapshot.Models) == 0 || len(snapshot.Models) > maxCapabilityModels || snapshot.Requests != len(snapshot.Models)+2 || snapshot.ResponseBytes <= 0 || snapshot.ResponseBytes > maxCapabilityResponseBytes {
		return fmt.Errorf("OpenRouter snapshot has invalid bounded request, response, or model counts")
	}
	modelIDs := make([]string, 0, len(snapshot.Models))
	for _, model := range snapshot.Models {
		modelIDs = append(modelIDs, model.ID)
		owner, name, canonical := strings.Cut(model.CanonicalSlug, "/")
		if model.ID == "" || !canonical || owner == "" || name == "" || strings.Contains(strings.ToLower(model.CanonicalSlug), "latest") || model.Name == "" || model.Created <= 0 || len(model.Endpoints) == 0 || len(model.Endpoints) > maxCapabilityEndpoints || !canonicalStrings(model.InputModalities) || !canonicalStrings(model.OutputModalities) || !slices.Contains(model.OutputModalities, "text") && !slices.Contains(model.OutputModalities, "transcription") {
			return fmt.Errorf("OpenRouter snapshot model %q has an invalid bounded identity or architecture", model.ID)
		}
		seen := make(map[string]struct{}, len(model.Endpoints))
		previous := ""
		requiresContext := slices.Contains(model.OutputModalities, "text")
		for _, endpoint := range model.Endpoints {
			key := endpoint.ProviderSlug + "\x00" + endpoint.Name
			if key < previous {
				return fmt.Errorf("OpenRouter snapshot model %q endpoints are not canonical", model.ID)
			}
			previous = key
			if _, exists := seen[key]; exists {
				return fmt.Errorf("OpenRouter snapshot model %q repeats endpoint %q", model.ID, endpoint.ProviderSlug)
			}
			seen[key] = struct{}{}
			if endpoint.Name == "" || endpoint.ModelID != model.ID || endpoint.ProviderName == "" || endpoint.ProviderSlug == "" || requiresContext && endpoint.ContextLength <= 0 || !requiresContext && endpoint.ContextLength < 0 || endpoint.MaxCompletionTokens < 0 || endpoint.MaxPromptTokens < 0 || !canonicalStrings(endpoint.SupportedParameters) || len(endpoint.Pricing) == 0 || len(endpoint.Pricing) > 32 {
				return fmt.Errorf("OpenRouter snapshot model %q has an invalid endpoint", model.ID)
			}
			for priceName, price := range endpoint.Pricing {
				if priceName == "" || len(priceName) > maxCapabilityFieldBytes || price == "" || len(price) > 128 {
					return fmt.Errorf("OpenRouter snapshot endpoint %q has invalid pricing", endpoint.ProviderSlug)
				}
				if _, err := fillereval.USDToNanoCeil(price); err != nil {
					return fmt.Errorf("OpenRouter snapshot endpoint %q price %q: %w", endpoint.ProviderSlug, priceName, err)
				}
			}
		}
	}
	if !canonicalStrings(modelIDs) {
		return fmt.Errorf("OpenRouter snapshot model identities are not canonical")
	}
	return nil
}

func canonicalStrings(values []string) bool {
	if len(values) == 0 || !slices.IsSorted(values) {
		return false
	}
	for i, value := range values {
		if value == "" || len(value) > maxCapabilityFieldBytes || i > 0 && values[i-1] == value {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
