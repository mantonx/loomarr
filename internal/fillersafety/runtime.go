package fillersafety

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// OpenRouterSemanticProfile is the certification-facing identity of one hosted rung. The
// request-specific body remains private; these values are enough to prove that certification and
// production use the same prompt, schema contract, modalities, and output ceiling.
type OpenRouterSemanticProfile struct {
	PromptSHA256        string
	SchemaSHA256        string
	Modalities          []string
	MaximumOutputTokens int64
}

// RuntimeProfile is the complete stable implementation identity a certification and projection
// authority must reproduce before a concrete evaluator can be constructed.
type RuntimeProfile struct {
	PolicySHA256             string
	ProposerSHA256           string
	EvaluationImplementation string
	Audio                    OpenRouterSemanticProfile
	Video                    OpenRouterSemanticProfile
}

// OpenRouterRuntimeProfile derives every implementation-owned identity rather than accepting
// hashes supplied by runtime configuration.
func OpenRouterRuntimeProfile(policy Policy) (RuntimeProfile, error) {
	if err := ValidatePolicy(policy); err != nil {
		return RuntimeProfile{}, ErrEvaluationInvalid
	}
	_, proposer := newCompleteAudioWindowProposer()
	return RuntimeProfile{
		PolicySHA256:             policySHA256(policy),
		ProposerSHA256:           proposerIdentitySHA256(proposer),
		EvaluationImplementation: evaluationImplementation,
		Audio: OpenRouterSemanticProfile{
			PromptSHA256: audioPromptSHA256(policy), SchemaSHA256: audioSchemaSHA256(policy),
			Modalities: []string{"audio"}, MaximumOutputTokens: audioMaxTokens,
		},
		Video: OpenRouterSemanticProfile{
			PromptSHA256: videoPromptSHA256(), SchemaSHA256: videoSchemaContractSHA256(),
			Modalities: []string{"audio", "video"}, MaximumOutputTokens: videoMaxTokens,
		},
	}, nil
}

// OpenRouterRouteConfig supplies only provider facts that implementation code cannot derive. A
// higher-level runtime must prove these facts against the certification authority and a fresh
// route snapshot before calling NewOpenRouterEvaluationOperation.
type OpenRouterRouteConfig struct {
	Model            string
	ResolvedModel    string
	UpstreamProvider string
	ProviderSlug     string
	CapabilitySHA256 string
	MaxChargeNanoUSD int64
	DisableReasoning bool
}

// OpenRouterEvaluationConfig contains the runtime-only dependencies for the certified cascade.
// It grants evidence execution only; the returned operation has no admission interface.
type OpenRouterEvaluationConfig struct {
	Repository ExecutionRepository
	Policy     Policy
	FFmpegPath string
	Client     *http.Client
	BaseURL    string
	APIKey     string
	Audio      OpenRouterRouteConfig
	Video      OpenRouterRouteConfig
	Budget     HostedCallBudget
	Now        func() time.Time
}

// NewOpenRouterEvaluationOperation composes the deterministic proposer, exact audio extraction,
// and both hosted rungs behind the one EvaluationOperation interface.
func NewOpenRouterEvaluationOperation(config OpenRouterEvaluationConfig) (EvaluationOperation, RuntimeProfile, error) {
	config.Policy = cloneRuntimePolicy(config.Policy)
	profile, err := OpenRouterRuntimeProfile(config.Policy)
	if err != nil || config.Repository == nil || config.Client == nil || config.Now == nil ||
		strings.TrimSpace(config.FFmpegPath) == "" || strings.TrimSpace(config.BaseURL) == "" ||
		strings.TrimSpace(config.APIKey) == "" || !validRuntimeRoute(config.Audio) || !validRuntimeRoute(config.Video) {
		return nil, RuntimeProfile{}, ErrEvaluationInvalid
	}
	proposer, identity := newCompleteAudioWindowProposer()
	audio := &openRouterAudioAdjudicator{config: openRouterAudioConfig{
		Client: config.Client, BaseURL: config.BaseURL, APIKey: config.APIKey,
		Model: config.Audio.Model, ResolvedModel: config.Audio.ResolvedModel,
		UpstreamProvider: config.Audio.UpstreamProvider, ProviderSlug: config.Audio.ProviderSlug,
		CapabilitySHA256: config.Audio.CapabilitySHA256, PromptSHA256: profile.Audio.PromptSHA256,
		Policy: config.Policy, PolicySHA256: profile.PolicySHA256,
		MaxChargeNanoUSD: config.Audio.MaxChargeNanoUSD, DisableReasoning: config.Audio.DisableReasoning,
	}}
	video := &openRouterVideoCorroborator{config: openRouterVideoConfig{
		Client: config.Client, BaseURL: config.BaseURL, APIKey: config.APIKey,
		Model: config.Video.Model, ResolvedModel: config.Video.ResolvedModel,
		UpstreamProvider: config.Video.UpstreamProvider, ProviderSlug: config.Video.ProviderSlug,
		CapabilitySHA256: config.Video.CapabilitySHA256, PromptSHA256: profile.Video.PromptSHA256,
		MaxChargeNanoUSD: config.Video.MaxChargeNanoUSD, DisableReasoning: config.Video.DisableReasoning,
	}}
	operation, err := newEvaluationOperation(config.Repository, evaluator{
		proposer: proposer, proposerIdentity: identity,
		audioExtractor: ffmpegCandidateAudioExtractor{path: config.FFmpegPath}, audio: audio, video: video,
	}, config.Budget, config.Now)
	if err != nil {
		return nil, RuntimeProfile{}, err
	}
	profile.Audio.Modalities = slices.Clone(profile.Audio.Modalities)
	profile.Video.Modalities = slices.Clone(profile.Video.Modalities)
	return operation, profile, nil
}

func cloneRuntimePolicy(policy Policy) Policy {
	cloned := policy
	cloned.Rules = slices.Clone(policy.Rules)
	for index := range cloned.Rules {
		cloned.Rules[index].Variants = slices.Clone(policy.Rules[index].Variants)
	}
	return cloned
}

func validRuntimeRoute(route OpenRouterRouteConfig) bool {
	return boundedAuthorityID(route.Model) && boundedAuthorityID(route.ResolvedModel) &&
		boundedAuthorityID(route.UpstreamProvider) && boundedAuthorityID(route.ProviderSlug) &&
		validSHA256(route.CapabilitySHA256) && route.MaxChargeNanoUSD > 0
}
