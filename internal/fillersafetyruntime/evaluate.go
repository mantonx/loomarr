package fillersafetyruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerairworthinessprojection"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func (r *Runtime) EvaluateSpokenSafety(ctx context.Context, request filler.SpokenSafetyProducerRequest) (fillersafety.EvaluationReport, error) {
	if r == nil || r.inspect == nil || r.build == nil || r.config.Repository == nil || ctx == nil || ctx.Err() != nil ||
		!validSHA256(request.OperationSHA256) || !validSubject(request.Subject) ||
		request.EvidencePath == "" || !filepath.IsAbs(request.EvidencePath) || filepath.Clean(request.EvidencePath) != request.EvidencePath ||
		request.EvidenceTool.Validate() != nil || request.Subject.EvidenceBytes > r.config.Deployment.MaximumSourceBytes ||
		request.Subject.DurationMS > r.config.Deployment.MaximumSourceDurationMS {
		return fillersafety.EvaluationReport{}, ErrRuntimeInvalid
	}
	existing, found, err := r.config.Repository.FindSpokenSafetyRun(ctx, request.OperationSHA256)
	if err != nil {
		return fillersafety.EvaluationReport{}, fmt.Errorf("find spoken-safety run: %w", err)
	}
	startedAt := r.config.Now().UTC()
	if found {
		startedAt = existing.CreatedAt.UTC()
	}
	if startedAt.IsZero() {
		return fillersafety.EvaluationReport{}, ErrRuntimeInvalid
	}
	inspection, err := r.inspect.Inspect(ctx, request.EvidencePath, r.config.FFmpegPath, request.EvidenceTool)
	if err != nil || inspection.Probe.DurationMs != request.Subject.DurationMS || inspection.Probe.Silent || inspection.Probe.NoVideo {
		return fillersafety.EvaluationReport{}, ErrRuntimeInvalid
	}
	ffmpeg, err := sourceToolIdentity(inspection.FFmpeg, "ffmpeg")
	if err != nil {
		return fillersafety.EvaluationReport{}, ErrRuntimeInvalid
	}
	ffprobe, err := sourceToolIdentity(inspection.FFprobe, "ffprobe")
	if err != nil {
		return fillersafety.EvaluationReport{}, ErrRuntimeInvalid
	}
	if !found {
		if err := r.validateFreshRoutes(ctx, startedAt); err != nil {
			return fillersafety.EvaluationReport{}, err
		}
	}
	operation, _, err := r.build(fillersafety.OpenRouterEvaluationConfig{
		Repository: r.config.Repository, Policy: r.config.Policy, FFmpegPath: r.config.FFmpegPath,
		Client: r.client, BaseURL: r.baseURL, APIKey: r.config.APIKey,
		Audio: runtimeRoute(r.authority.AudioRoute, r.config.Deployment.AudioReservationNanoUSD),
		Video: runtimeRoute(r.authority.VideoRoute, r.config.Deployment.VideoReservationNanoUSD),
		Budget: fillersafety.HostedCallBudget{
			PerClipNanoUSD: r.config.Deployment.PerClipBudgetNanoUSD,
			PerDayNanoUSD:  r.config.Deployment.PerDayBudgetNanoUSD,
			PerRunNanoUSD:  r.config.Deployment.PerRunBudgetNanoUSD,
		},
		Now: r.config.Now,
	})
	if err != nil {
		return fillersafety.EvaluationReport{}, ErrRuntimeInvalid
	}
	report, err := operation.Evaluate(ctx, fillersafety.EvaluationRequest{
		RunID: request.OperationSHA256, StartedAt: startedAt,
		CertificationSHA256: r.authoritySHA256,
		Source: fillersafety.SourceRequest{
			Path: request.EvidencePath,
			Authority: fillersafety.SourceAuthority{
				SchemaVersion: fillersafety.SourceAuthoritySchemaVersion,
				PolicySHA256:  r.profile.PolicySHA256, Implementation: sourceAuthorityImplementation,
				SourceID: request.Subject.SHA256, SourceSHA256: request.Subject.EvidenceSHA256,
				SourceBytes: request.Subject.EvidenceBytes, DurationMS: request.Subject.DurationMS,
				HasAudio: true, HasVideo: true, MeasuredAt: startedAt, FFmpeg: ffmpeg, FFprobe: ffprobe,
			},
		},
	})
	if err != nil {
		return fillersafety.EvaluationReport{}, err
	}
	if report.Run.ID != request.OperationSHA256 {
		return fillersafety.EvaluationReport{}, ErrRuntimeInvalid
	}
	return report, nil
}

func validSubject(subject fillerairworthinessprojection.Subject) bool {
	return validSHA256(subject.SHA256) && validSHA256(subject.EvidenceSHA256) &&
		subject.EvidenceBytes > 0 && subject.DurationMS > 0
}

func runtimeRoute(route fillersafetycert.RouteAuthority, reservation int64) fillersafety.OpenRouterRouteConfig {
	return fillersafety.OpenRouterRouteConfig{
		Model: route.RequestedModel, ResolvedModel: route.ResolvedModel,
		UpstreamProvider: route.UpstreamProvider, ProviderSlug: route.UpstreamProviderSlug,
		CapabilitySHA256: route.CapabilitySHA256, MaxChargeNanoUSD: reservation,
		DisableReasoning: route.ReasoningMode == fillersafetycert.ReasoningDisabled,
	}
}

func (r *Runtime) validateFreshRoutes(ctx context.Context, at time.Time) error {
	snapshot, err := r.freshSnapshot(ctx, at)
	if err != nil {
		return fmt.Errorf("refresh spoken-safety route authority: %w", err)
	}
	if err := validateCurrentRoute(snapshot, r.authority.AudioRoute, r.profile.Audio, at,
		r.config.Deployment.AudioMaximumInputTokens, r.config.Deployment.AudioReservationNanoUSD, true); err != nil {
		return fmt.Errorf("validate spoken-safety audio route: %w", err)
	}
	if err := validateCurrentRoute(snapshot, r.authority.VideoRoute, r.profile.Video, at,
		r.config.Deployment.VideoMaximumInputTokens, r.config.Deployment.VideoReservationNanoUSD, false); err != nil {
		return fmt.Errorf("validate spoken-safety video route: %w", err)
	}
	return nil
}

func (r *Runtime) freshSnapshot(ctx context.Context, at time.Time) (fillerbakeoff.OpenRouterSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.snapshot.RetrievedAt.IsZero() {
		age := at.Sub(r.snapshot.RetrievedAt)
		if age >= 0 && age < snapshotRefreshAge {
			return r.snapshot, nil
		}
	}
	models := []string{r.authority.AudioRoute.RequestedModel, r.authority.VideoRoute.RequestedModel}
	slices.Sort(models)
	models = slices.Compact(models)
	snapshot, err := r.config.FetchSnapshot(ctx, fillerbakeoff.OpenRouterSnapshotConfig{
		BaseURL: fillerbakeoff.OpenRouterBaseURL, APIKey: r.config.APIKey,
		Models: models, RetrievedAt: at, Client: r.client,
	})
	if err != nil {
		return fillerbakeoff.OpenRouterSnapshot{}, err
	}
	r.snapshot = snapshot
	return snapshot, nil
}

func validateCurrentRoute(snapshot fillerbakeoff.OpenRouterSnapshot, route fillersafetycert.RouteAuthority,
	profile fillersafety.OpenRouterSemanticProfile, at time.Time, maximumInputTokens, reservation int64, audio bool,
) error {
	var model fillerbakeoff.OpenRouterModelSnapshot
	var endpoint fillerbakeoff.OpenRouterEndpointSnapshot
	var err error
	if audio {
		model, endpoint, err = fillerbakeoff.ValidateOpenRouterAudioRoute(snapshot, route.RequestedModel,
			route.UpstreamProvider, route.UpstreamProviderSlug, at, profile.MaximumOutputTokens)
	} else {
		model, endpoint, err = fillerbakeoff.ValidateOpenRouterVideoRoute(snapshot, route.RequestedModel,
			route.UpstreamProvider, route.UpstreamProviderSlug, at, profile.MaximumOutputTokens)
	}
	if err != nil || model.CanonicalSlug != route.ResolvedModel {
		return ErrRuntimeInvalid
	}
	_, capabilitySHA256, err := fillerbakeoff.OpenRouterAssessorIdentity(snapshot, route.RequestedModel,
		route.UpstreamProvider, route.UpstreamProviderSlug, route.ReasoningMode)
	if err != nil || capabilitySHA256 != route.CapabilitySHA256 {
		return ErrRuntimeInvalid
	}
	maximumCharge, err := fillerbakeoff.EstimateOpenRouterTokenChargeNanoUSD(endpoint, maximumInputTokens, profile.MaximumOutputTokens)
	if err != nil || maximumCharge > reservation {
		return ErrRuntimeInvalid
	}
	return nil
}
