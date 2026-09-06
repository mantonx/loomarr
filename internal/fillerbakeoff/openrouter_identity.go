package fillerbakeoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const openRouterAssessorIdentityVersion = 1

type openRouterModelIdentity struct {
	Version       int    `json:"version"`
	SourceBaseURL string `json:"sourceBaseUrl"`
	RequestedID   string `json:"requestedId"`
	CanonicalSlug string `json:"canonicalSlug"`
	Created       int64  `json:"created"`
}

type openRouterCapabilityIdentity struct {
	Version                 int                     `json:"version"`
	Model                   openRouterModelIdentity `json:"model"`
	InputModalities         []string                `json:"inputModalities"`
	OutputModalities        []string                `json:"outputModalities"`
	UpstreamProvider        string                  `json:"upstreamProvider"`
	UpstreamProviderSlug    string                  `json:"upstreamProviderSlug"`
	Quantization            string                  `json:"quantization"`
	ContextLength           int64                   `json:"contextLength"`
	MaxCompletionTokens     int64                   `json:"maxCompletionTokens"`
	MaxPromptTokens         int64                   `json:"maxPromptTokens"`
	SupportedParameters     []string                `json:"supportedParameters"`
	ZDR                     bool                    `json:"zdr"`
	SupportsImplicitCaching bool                    `json:"supportsImplicitCaching"`
	InferenceMode           string                  `json:"inferenceMode"`
}

// OpenRouterAssessorIdentity returns stable model and selected-route capability digests. It
// deliberately excludes capture time, liveness, prices, and response accounting; callers retain
// the complete snapshot digest separately and must still apply live freshness and route checks.
func OpenRouterAssessorIdentity(snapshot OpenRouterSnapshot, modelID, upstreamProvider, upstreamProviderSlug, inferenceMode string) (string, string, error) {
	if err := ValidateOpenRouterSnapshot(snapshot); err != nil {
		return "", "", err
	}
	model, ok := snapshotModel(snapshot, modelID)
	if !ok {
		return "", "", fmt.Errorf("OpenRouter model %q is absent from the snapshot", modelID)
	}
	endpoint, ok := snapshotEndpoint(model, upstreamProviderSlug, upstreamProvider)
	if !ok {
		return "", "", fmt.Errorf("OpenRouter endpoint %q/%q is absent from the snapshot", upstreamProvider, upstreamProviderSlug)
	}
	if inferenceMode == "" {
		return "", "", fmt.Errorf("OpenRouter assessor inference mode is empty")
	}
	modelIdentity := openRouterModelIdentity{
		Version: openRouterAssessorIdentityVersion, SourceBaseURL: snapshot.SourceBaseURL,
		RequestedID: model.ID, CanonicalSlug: model.CanonicalSlug, Created: model.Created,
	}
	capabilityIdentity := openRouterCapabilityIdentity{
		Version: openRouterAssessorIdentityVersion, Model: modelIdentity,
		InputModalities: append([]string(nil), model.InputModalities...), OutputModalities: append([]string(nil), model.OutputModalities...),
		UpstreamProvider: endpoint.ProviderName, UpstreamProviderSlug: endpoint.ProviderSlug,
		Quantization: endpoint.Quantization, ContextLength: endpoint.ContextLength,
		MaxCompletionTokens: endpoint.MaxCompletionTokens, MaxPromptTokens: endpoint.MaxPromptTokens,
		SupportedParameters: append([]string(nil), endpoint.SupportedParameters...), ZDR: endpoint.ZDR,
		SupportsImplicitCaching: endpoint.SupportsImplicitCache, InferenceMode: inferenceMode,
	}
	return openRouterIdentitySHA256(modelIdentity), openRouterIdentitySHA256(capabilityIdentity), nil
}

func openRouterIdentitySHA256(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
