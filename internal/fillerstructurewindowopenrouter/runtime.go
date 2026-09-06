package fillerstructurewindowopenrouter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/httpx"
)

const (
	MaximumOutputTokens = 1024
	snapshotRefreshAge  = 12 * time.Hour
)

type SnapshotFetcher func(context.Context, fillerbakeoff.OpenRouterSnapshotConfig) (fillerbakeoff.OpenRouterSnapshot, error)

type CertifiedRuntimeConfig struct {
	Authority     fillerstructurewindow.MaterializationAuthority
	Deployment    Deployment
	APIKey        string
	SourceRoot    string
	MediaRoot     string
	EvidenceRoot  string
	FFmpegPath    string
	Ledger        Ledger
	Client        *http.Client
	Now           func() time.Time
	FetchSnapshot SnapshotFetcher
}

// CertifiedRuntime lazily refreshes live route metadata, proves it reproduces the reviewed
// profiles, then delegates one source to the provider-neutral family-major window runtime.
type CertifiedRuntime struct {
	config   CertifiedRuntimeConfig
	preparer filler.StructureAssessmentWindowMediaPreparer
	evidence *filler.FileStructureAssessmentEvidenceRepository
	client   *http.Client

	mu       sync.Mutex
	snapshot fillerbakeoff.OpenRouterSnapshot
}

func NewCertifiedRuntime(config CertifiedRuntimeConfig) (*CertifiedRuntime, error) {
	if err := ValidateDeployment(config.Deployment, config.Authority); err != nil {
		return nil, err
	}
	if config.APIKey == "" || config.Ledger == nil || !cleanAbsolutePath(config.SourceRoot) || !cleanAbsolutePath(config.MediaRoot) ||
		!cleanAbsolutePath(config.EvidenceRoot) || config.MediaRoot == config.EvidenceRoot || config.FFmpegPath == "" {
		return nil, errors.New("structure window certified runtime requires credential, distinct storage roots, ffmpeg, and ledger")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	config.Now = now
	if config.FetchSnapshot == nil {
		config.FetchSnapshot = fillerbakeoff.FetchOpenRouterSnapshot
	}
	config.Authority.Assessors = slices.Clone(config.Authority.Assessors)
	config.Authority.AllowedUnits = slices.Clone(config.Authority.AllowedUnits)
	config.Authority.AllowedRoles = slices.Clone(config.Authority.AllowedRoles)
	config.Deployment.Families = slices.Clone(config.Deployment.Families)
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-structure-window-openrouter", httpx.TimeoutLLM)
	}
	preparer, err := filler.NewFFmpegStructureAssessmentMediaPreparer(config.SourceRoot, config.MediaRoot, config.FFmpegPath)
	if err != nil {
		return nil, err
	}
	evidence, err := filler.NewFileStructureAssessmentEvidenceRepository(config.EvidenceRoot)
	if err != nil {
		return nil, err
	}
	return &CertifiedRuntime{config: config, preparer: preparer, evidence: evidence, client: client}, nil
}

func (r *CertifiedRuntime) Assess(ctx context.Context, input filler.StructureAssessmentSource) (fillerstructure.Artifact, error) {
	if r == nil || r.preparer == nil || r.evidence == nil || r.client == nil {
		return fillerstructure.Artifact{}, errors.New("structure window certified runtime is unavailable")
	}
	if input.Source.DurationMs < r.config.Authority.MinimumSourceDurationMS ||
		input.Source.DurationMs > r.config.Authority.MaximumSourceDurationMS {
		return fillerstructure.Artifact{}, errors.New("structure window source is outside the reviewed duration envelope")
	}
	source := fillerstructure.Source{SHA256: input.Source.SHA256, Bytes: input.Source.Bytes, DurationMS: input.Source.DurationMs}
	plan, err := fillerstructurewindow.NewPlan(source)
	if err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("plan structure windows: %w", err)
	}
	prepared, err := r.preparer.PrepareWindows(ctx, input, plan)
	if err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("preflight structure window media: %w", err)
	}
	if err := filler.ValidatePreparedStructureAssessmentWindows(input, prepared); err != nil {
		return fillerstructure.Artifact{}, err
	}
	if err := validatePreparedMediaAuthority(prepared, r.config.Authority); err != nil {
		return fillerstructure.Artifact{}, err
	}
	snapshot, err := r.freshSnapshot(ctx)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	assessors, err := r.assessors(snapshot, r.config.Now().UTC())
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	runtime, err := filler.NewStructureWindowAssessmentRuntime(
		assessors, r.preparer, r.evidence, r.config.Authority.BoundaryToleranceMS, r.config.Now,
	)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	artifact, err := runtime.AssessPrepared(ctx, input, prepared)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	return artifact, nil
}

func validatePreparedMediaAuthority(prepared filler.StructureAssessmentWindowMediaSet, authority fillerstructurewindow.MaterializationAuthority) error {
	if err := fillerstructurewindow.ValidateMediaSet(prepared.Authority); err != nil ||
		prepared.Authority.Plan.Profile.SHA256 != authority.WindowProfileSHA256 ||
		prepared.Authority.Plan.Profile.AssessmentMediaProfileSHA256 != authority.AssessmentMediaProfileSHA256 ||
		len(prepared.Windows) == 0 || len(prepared.Windows) > authority.MaximumWindows ||
		len(prepared.Windows) != len(prepared.Authority.Windows) {
		return errors.New("prepared structure window media is outside the reviewed authority")
	}
	for ordinal, window := range prepared.Windows {
		if window.Media != prepared.Authority.Windows[ordinal] || window.Media.Media.Bytes > authority.MaximumWindowBytes {
			return errors.New("prepared structure window media is outside the reviewed byte envelope")
		}
	}
	return nil
}

func (r *CertifiedRuntime) freshSnapshot(ctx context.Context) (fillerbakeoff.OpenRouterSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.config.Now().UTC()
	if !r.snapshot.RetrievedAt.IsZero() {
		age := now.Sub(r.snapshot.RetrievedAt)
		if age >= 0 && age < snapshotRefreshAge {
			return r.snapshot, nil
		}
	}
	models := make([]string, 0, len(r.config.Deployment.Families))
	for _, family := range r.config.Deployment.Families {
		models = append(models, family.Model)
	}
	slices.Sort(models)
	models = slices.Compact(models)
	snapshot, err := r.config.FetchSnapshot(ctx, fillerbakeoff.OpenRouterSnapshotConfig{
		BaseURL: fillerbakeoff.OpenRouterBaseURL, APIKey: r.config.APIKey, Models: models,
		RetrievedAt: now, Client: r.client,
	})
	if err != nil {
		return fillerbakeoff.OpenRouterSnapshot{}, fmt.Errorf("refresh OpenRouter structure metadata: %w", err)
	}
	r.snapshot = snapshot
	return snapshot, nil
}

func (r *CertifiedRuntime) assessors(snapshot fillerbakeoff.OpenRouterSnapshot, at time.Time) ([]filler.CompleteWindowStructureAssessor, error) {
	snapshotSHA := fillerbakeoff.OpenRouterSnapshotSHA256(snapshot)
	result := make([]filler.CompleteWindowStructureAssessor, 0, len(r.config.Deployment.Families))
	profiles := make([]fillerstructure.AssessorProfile, 0, len(r.config.Deployment.Families))
	for _, family := range r.config.Deployment.Families {
		model, endpoint, err := fillerbakeoff.ValidateOpenRouterVideoRoute(
			snapshot, family.Model, family.UpstreamProvider, family.UpstreamProviderSlug, at, MaximumOutputTokens,
		)
		if err != nil {
			return nil, err
		}
		modelDigest, capabilitySHA, err := fillerbakeoff.OpenRouterAssessorIdentity(
			snapshot, family.Model, family.UpstreamProvider, family.UpstreamProviderSlug, family.ReasoningMode,
		)
		if err != nil {
			return nil, err
		}
		profile := fillerstructure.AssessorProfile{
			ID: family.AssessorID, ModelFamily: family.ModelFamily, Provider: "openrouter", Model: family.Model,
			ModelDigest: modelDigest, CapabilitySHA256: capabilitySHA,
			PromptVersion: fillerstructurewindow.DirectVideoPromptVersion, EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
		}
		maximumCharge, err := fillerbakeoff.EstimateOpenRouterTokenChargeNanoUSD(endpoint, family.MaximumInputTokens, MaximumOutputTokens)
		if err != nil {
			return nil, err
		}
		if maximumCharge > family.ReservationNanoUSD {
			return nil, fmt.Errorf("OpenRouter structure reservation %d is below current price bound %d", family.ReservationNanoUSD, maximumCharge)
		}
		assessor, err := New(Config{
			Profile: profile, MetadataSnapshotSHA256: snapshotSHA,
			APIKey: r.config.APIKey, BaseURL: fillerbakeoff.OpenRouterBaseURL, Model: family.Model,
			ResolvedModel: model.CanonicalSlug, UpstreamProvider: family.UpstreamProvider,
			UpstreamProviderSlug: family.UpstreamProviderSlug, ReservationNanoUSD: family.ReservationNanoUSD,
			MaximumChargeNanoUSD: maximumCharge, MaxTokens: MaximumOutputTokens,
			DisableReasoning: family.ReasoningMode == ReasoningDisabled,
			EnableReasoning:  family.ReasoningMode == ReasoningProviderRequired,
			Client:           r.client, Ledger: r.config.Ledger, Now: r.config.Now,
		})
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
		result = append(result, assessor)
	}
	if !reflect.DeepEqual(profiles, r.config.Authority.Assessors) {
		return nil, errors.New("live OpenRouter structure profiles do not match reviewed authority")
	}
	return result, nil
}

func cleanAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

var _ filler.CompleteTimelineStructureDecisioner = (*CertifiedRuntime)(nil)
