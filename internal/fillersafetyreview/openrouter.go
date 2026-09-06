package fillersafetyreview

import (
	"context"
	"encoding/base64"
	"errors"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

func reviewOne(
	ctx context.Context,
	runtime reviewRuntime,
	loaded loadedInputs,
	item fillersafetycorpus.ReviewWorklistCase,
	wav []byte,
	apiKey string,
	reserve func(string) error,
) (modelObservation, fillersafetycert.ReviewAssessment, openroutermedia.Result, string, error) {
	content, err := reviewContent(loaded.policy, item)
	if err != nil {
		return modelObservation{}, fillersafetycert.ReviewAssessment{}, openroutermedia.Result{}, failureInvalidResponse, err
	}
	authority, err := reviewRouteAuthority(loaded, runtime)
	if err != nil {
		return modelObservation{}, fillersafetycert.ReviewAssessment{}, openroutermedia.Result{}, failureProvider, err
	}
	transport, err := runtime.call(ctx, runtime.client, runtime.baseURL, openroutermedia.Config{
		Authority: authority,
		APIKey:    apiKey, Model: loaded.plan.Model, ResolvedModel: loaded.plan.ResolvedModel,
		UpstreamProvider: loaded.plan.UpstreamProvider, ProviderSlug: loaded.plan.UpstreamProviderSlug,
		SchemaName: "spoken_safety_independent_review", Schema: reviewSchema(loaded.policy),
		SystemPrompt: reviewSystemPrompt, Content: content,
		Audios:    []openroutermedia.Audio{{Format: "wav", Base64: base64.StdEncoding.EncodeToString(wav)}},
		MaxTokens: reviewMaxTokens, ReservationNanoUSD: loaded.plan.MaximumChargeNanoUSD,
		DisableReasoning: loaded.plan.DisableReasoning, Title: "Loomarr spoken-safety independent review",
		Reserve: reserve,
	})
	if err != nil {
		return modelObservation{}, fillersafetycert.ReviewAssessment{}, transport, failureProvider, err
	}
	observation, err := decodeObservation(transport.StructuredOutput)
	if err != nil {
		return modelObservation{}, fillersafetycert.ReviewAssessment{}, transport, failureInvalidResponse, err
	}
	assessment, err := assessmentFromObservation(item, loaded.policy, observation)
	if err != nil {
		failure := failureInvalidResponse
		if errors.Is(err, errReviewUnclear) {
			failure = failureUnclear
		}
		return observation, fillersafetycert.ReviewAssessment{}, transport, failure, err
	}
	return observation, assessment, transport, "", nil
}

func reviewRouteAuthority(loaded loadedInputs, runtime reviewRuntime) (openroutermedia.RouteAuthority, error) {
	return openroutermedia.NewRouteAuthority(
		loaded.snapshot, openroutermedia.CapabilitySnapshotSHA256(loaded.snapshot), openroutermedia.RouteRequirements{
			BaseURL: runtime.baseURL, RequestedModel: loaded.plan.Model, CanonicalModel: loaded.plan.ResolvedModel,
			UpstreamProvider: loaded.plan.UpstreamProvider, ProviderSlug: loaded.plan.UpstreamProviderSlug,
			RequiredInputModalities: []string{"audio", "text"}, MaxTokens: reviewMaxTokens,
			RequireReasoning: !loaded.plan.DisableReasoning, Now: runtime.now,
		},
	)
}
