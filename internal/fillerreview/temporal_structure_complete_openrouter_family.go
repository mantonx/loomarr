package fillerreview

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructureopenrouter"
	"github.com/loomarr/loomarr/internal/httpx"
)

type TemporalStructureCompleteOpenRouterFamilyConfig struct {
	BaseURL              string
	APIKey               string
	Snapshot             fillerbakeoff.OpenRouterSnapshot
	Model                string
	ModelFamily          string
	UpstreamProvider     string
	UpstreamProviderSlug string
	AssessorID           string
	ReasoningMode        string
	ReservationNanoUSD   int64
	MaximumInputTokens   int64
	EvidenceRoot         string
	Ledger               fillerstructureopenrouter.Ledger
	Client               *http.Client
	AllowInsecureTestURL bool
	Now                  func() time.Time
}

type TemporalStructureCompleteOpenRouterFamily struct {
	Runtime                       *filler.StructureCompleteFamilyRuntime
	Profile                       fillerstructure.AssessorProfile
	EstimatedMaximumChargeNanoUSD int64
}

func NewTemporalStructureCompleteOpenRouterFamily(config TemporalStructureCompleteOpenRouterFamilyConfig) (TemporalStructureCompleteOpenRouterFamily, error) {
	baseURL, client, now, err := validateTemporalStructureCompleteOpenRouterFamilyConfig(config)
	if err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	check := TemporalStructureOpenRouterConfig{
		BaseURL: baseURL, Snapshot: config.Snapshot, Model: config.Model, ModelFamily: config.ModelFamily,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		MaximumInputTokens: config.MaximumInputTokens,
	}
	if err := validateTemporalStructureOpenRouterSnapshot(check, baseURL, now().UTC()); err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	estimated, err := estimateTemporalStructureOpenRouterCharge(check)
	if err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	if estimated > config.ReservationNanoUSD {
		return TemporalStructureCompleteOpenRouterFamily{}, fmt.Errorf("OpenRouter complete-video accounting reservation %d nano-USD is below the snapshot price bound %d", config.ReservationNanoUSD, estimated)
	}
	modelDigest, capabilitySHA, err := fillerbakeoff.OpenRouterAssessorIdentity(
		config.Snapshot, config.Model, config.UpstreamProvider, config.UpstreamProviderSlug, config.ReasoningMode,
	)
	if err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	profile := fillerstructure.AssessorProfile{
		ID: config.AssessorID, Provider: "openrouter", Model: config.Model, ModelFamily: config.ModelFamily,
		ModelDigest: modelDigest, CapabilitySHA256: capabilitySHA,
		PromptVersion: fillerstructure.DirectVideoPromptVersion, EvidenceContract: fillerstructure.AssessmentRecordContractVersion,
	}
	if err := fillerstructure.ValidateAssessorProfile(profile); err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	assessor, err := fillerstructureopenrouter.New(fillerstructureopenrouter.Config{
		Profile: profile, MetadataSnapshotSHA256: fillerbakeoff.OpenRouterSnapshotSHA256(config.Snapshot),
		APIKey: config.APIKey, BaseURL: baseURL, Model: config.Model,
		ResolvedModel: model.CanonicalSlug, UpstreamProvider: config.UpstreamProvider,
		UpstreamProviderSlug: config.UpstreamProviderSlug, ReservationNanoUSD: config.ReservationNanoUSD,
		MaximumChargeNanoUSD: estimated, MaxTokens: temporalStructureOpenRouterMaxTokens,
		DisableReasoning:     config.ReasoningMode == TemporalStructureOpenRouterReasoningDisabled,
		EnableReasoning:      config.ReasoningMode == TemporalStructureOpenRouterReasoningRequired,
		AllowInsecureTestURL: config.AllowInsecureTestURL, Client: client, Ledger: config.Ledger, Now: now,
	})
	if err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	evidence, err := filler.NewFileStructureAssessmentEvidenceRepository(config.EvidenceRoot)
	if err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	runtime, err := filler.NewStructureCompleteFamilyRuntime(assessor, evidence)
	if err != nil {
		return TemporalStructureCompleteOpenRouterFamily{}, err
	}
	return TemporalStructureCompleteOpenRouterFamily{Runtime: runtime, Profile: profile, EstimatedMaximumChargeNanoUSD: estimated}, nil
}

func validateTemporalStructureCompleteOpenRouterFamilyConfig(config TemporalStructureCompleteOpenRouterFamilyConfig) (string, *http.Client, func() time.Time, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = fillerbakeoff.OpenRouterBaseURL
	}
	parsed, err := url.Parse(baseURL)
	loopback := err == nil && config.AllowInsecureTestURL && reviewLoopback(parsed.Hostname())
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(!loopback && (parsed.Scheme != "https" || parsed.Hostname() != "openrouter.ai" || parsed.Path != "/api/v1")) {
		return "", nil, nil, errors.New("OpenRouter complete family requires the canonical HTTPS API base")
	}
	if strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.Model) == "" ||
		strings.Contains(strings.ToLower(config.Model), "latest") || strings.TrimSpace(config.ModelFamily) == "" ||
		strings.TrimSpace(config.UpstreamProvider) == "" || strings.TrimSpace(config.UpstreamProviderSlug) == "" ||
		strings.TrimSpace(config.AssessorID) == "" || !validTemporalStructureOpenRouterReasoningMode(config.ReasoningMode) ||
		config.ReservationNanoUSD <= 0 || config.MaximumInputTokens <= 0 || config.Ledger == nil ||
		!filepath.IsAbs(config.EvidenceRoot) || filepath.Clean(config.EvidenceRoot) != config.EvidenceRoot {
		return "", nil, nil, errors.New("OpenRouter complete family requires exact route, assessor, reasoning, accounting, ledger, and evidence identity")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-temporal-structure-complete-openrouter", httpx.TimeoutLLM)
	}
	copyClient := *client
	copyClient.Timeout = 0
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return baseURL, &copyClient, now, nil
}
