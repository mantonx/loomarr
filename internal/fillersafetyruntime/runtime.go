package fillersafetyruntime

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerairworthinessprojection"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/httpx"
)

const (
	sourceAuthorityImplementation = "filler-spoken-rendered-child-source-v1"
	snapshotRefreshAge            = 12 * time.Hour
)

var ErrRuntimeInvalid = errors.New("spoken-safety certified runtime is invalid")

type SnapshotFetcher func(context.Context, fillerbakeoff.OpenRouterSnapshotConfig) (fillerbakeoff.OpenRouterSnapshot, error)

type Config struct {
	Certification fillersafetycert.RuntimeAuthority
	Projection    fillerairworthinessprojection.SpokenAuthority
	Policy        fillersafety.Policy
	Deployment    Deployment
	Repository    fillersafety.ExecutionRepository
	APIKey        string
	FFmpegPath    string
	Client        *http.Client
	Now           func() time.Time
	FetchSnapshot SnapshotFetcher
}

// Runtime is the concrete evidence producer consumed by rendered-child screening. Its only
// externally useful behavior is the filler.SpokenSafetyProducer interface.
type Runtime struct {
	config          Config
	authority       fillersafetycert.Authority
	authoritySHA256 string
	profile         fillersafety.RuntimeProfile
	client          *http.Client
	inspect         sourceInspector
	build           operationFactory
	baseURL         string

	mu       sync.Mutex
	snapshot fillerbakeoff.OpenRouterSnapshot
}

type operationFactory func(fillersafety.OpenRouterEvaluationConfig) (fillersafety.EvaluationOperation, fillersafety.RuntimeProfile, error)

var _ filler.SpokenSafetyProducer = (*Runtime)(nil)

func New(config Config) (*Runtime, error) {
	return newRuntime(config, realSourceInspector{}, fillerbakeoff.OpenRouterBaseURL)
}

func newRuntime(config Config, inspector sourceInspector, baseURL string) (*Runtime, error) {
	authority := config.Certification.Authority()
	return newRuntimeWithAuthority(config, authority, config.Certification.AuthoritySHA256(), inspector, baseURL)
}

func newRuntimeWithAuthority(config Config, authority fillersafetycert.Authority, authoritySHA256 string, inspector sourceInspector, baseURL string) (*Runtime, error) {
	config.Policy = clonePolicy(config.Policy)
	config.Projection.Rules = slices.Clone(config.Projection.Rules)
	profile, err := fillersafety.OpenRouterRuntimeProfile(config.Policy)
	if err != nil || inspector == nil || config.Repository == nil || config.APIKey == "" || config.FFmpegPath == "" ||
		baseURL == "" || validateDeploymentShape(config.Deployment) != nil ||
		config.Deployment.AuthoritySHA256 != authoritySHA256 ||
		validateJoinedAuthorities(authority, authoritySHA256, config.Projection, config.Policy, profile) != nil ||
		validateDeploymentEnvelope(config.Deployment, authority) != nil {
		return nil, ErrRuntimeInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.FetchSnapshot == nil {
		config.FetchSnapshot = fillerbakeoff.FetchOpenRouterSnapshot
	}
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-spoken-safety-openrouter", httpx.TimeoutLLM)
	}
	return &Runtime{
		config: config, authority: authority, authoritySHA256: authoritySHA256, profile: profile, client: client,
		inspect: inspector, build: fillersafety.NewOpenRouterEvaluationOperation, baseURL: baseURL,
	}, nil
}

func clonePolicy(policy fillersafety.Policy) fillersafety.Policy {
	cloned := policy
	cloned.Rules = slices.Clone(policy.Rules)
	for index := range cloned.Rules {
		cloned.Rules[index].Variants = slices.Clone(policy.Rules[index].Variants)
	}
	return cloned
}

func validateJoinedAuthorities(authority fillersafetycert.Authority, authoritySHA256 string,
	projection fillerairworthinessprojection.SpokenAuthority, policy fillersafety.Policy,
	profile fillersafety.RuntimeProfile,
) error {
	if fillerairworthinessprojection.ValidateSpokenAuthority(projection) != nil ||
		authority.ChallengeKind != fillersafetycert.ChallengeCertification ||
		authority.PolicySHA256 != profile.PolicySHA256 || authority.ProposerSHA256 != profile.ProposerSHA256 ||
		authority.Implementation != profile.EvaluationImplementation ||
		projection.PolicySHA256 != profile.PolicySHA256 || projection.CertificationSHA256 != authoritySHA256 ||
		projection.ProposerSHA256 != profile.ProposerSHA256 || projection.EvaluationImplementation != profile.EvaluationImplementation ||
		!routeMatchesProfile(authority.AudioRoute, profile.Audio, "native-audio") ||
		!routeMatchesProfile(authority.VideoRoute, profile.Video, "complete-video") {
		return ErrRuntimeInvalid
	}
	policyRules := make([]string, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		policyRules = append(policyRules, rule.ID)
	}
	projectionRules := make([]string, 0, len(projection.Rules))
	for _, rule := range projection.Rules {
		projectionRules = append(projectionRules, rule.ID)
	}
	slices.Sort(policyRules)
	slices.Sort(projectionRules)
	if !slices.Equal(policyRules, projectionRules) {
		return ErrRuntimeInvalid
	}
	return nil
}

func routeMatchesProfile(route fillersafetycert.RouteAuthority, profile fillersafety.OpenRouterSemanticProfile, rung string) bool {
	return route.Role == "spoken-safety" && route.Rung == rung &&
		route.RequestedProvider == "openrouter" && route.ResolvedProvider == "openrouter" &&
		route.ReasoningMode == fillersafetycert.ReasoningDisabled &&
		route.PromptSHA256 == profile.PromptSHA256 && route.SchemaSHA256 == profile.SchemaSHA256 &&
		slices.Equal(route.Modalities, profile.Modalities)
}

func validateDeploymentEnvelope(deployment Deployment, authority fillersafetycert.Authority) error {
	var maximumBytes, maximumDuration int64
	for _, item := range authority.Cases {
		maximumBytes = max(maximumBytes, item.SourceBytes)
		maximumDuration = max(maximumDuration, item.DurationMS)
	}
	if deployment.MaximumSourceBytes > maximumBytes || deployment.MaximumSourceDurationMS > maximumDuration {
		return ErrRuntimeInvalid
	}
	return nil
}
