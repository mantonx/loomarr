package fillerreview

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	temporalStructureOpenRouterSchemaName = "filler_temporal_structure"
	temporalStructureOpenRouterMaxTokens  = 4096
	temporalStructureOpenRouterTitle      = "Loomarr temporal structure challenge"
)

func assessOpenRouterTemporalStructureCase(ctx context.Context, client *http.Client, baseURL, root string, config TemporalStructureOpenRouterConfig, item TemporalStructureChallengePublicCase, checkpoint *temporalStructureOpenRouterCheckpoint, selected []TemporalStructureChallengePublicCase, now func() time.Time) (TemporalStructureAssessment, error) {
	caseCtx, cancel := context.WithTimeout(ctx, config.PerCaseTimeout)
	defer cancel()
	videoPath := filepath.Join(root, filepath.FromSlash(item.Video.Path))
	video, err := os.ReadFile(videoPath)
	if err != nil || int64(len(video)) != item.Video.Bytes || hashBytes(video) != item.Video.SHA256 || len(video) == 0 || int64(len(video)) > TemporalStructureOpenRouterMaximumVideoBytes {
		return TemporalStructureAssessment{}, fmt.Errorf("verified structure video for alias %q is unavailable, drifted, or outside its byte ceiling", item.Alias)
	}
	started := time.Now()
	callResult, callErr := callOpenRouterStructured(caseCtx, client, baseURL, openRouterStructuredCallConfig{
		APIKey: config.APIKey, Model: config.Model, ResolvedModel: checkpoint.Identity.ResolvedModel,
		UpstreamProvider: config.UpstreamProvider, ProviderSlug: config.UpstreamProviderSlug,
		SchemaName: temporalStructureOpenRouterSchemaName, Schema: temporalStructureOpenRouterSchema(item.Video.DurationMS),
		SystemPrompt: temporalStructureOpenRouterSystemPrompt, Content: temporalStructureOpenRouterContent(item.Video.DurationMS),
		Videos:    []openRouterStructuredVideo{{MIMEType: "video/mp4", Base64: base64.StdEncoding.EncodeToString(video)}},
		MaxTokens: temporalStructureOpenRouterMaxTokens, MaxChargeNanoUSD: config.MaxChargeNanoUSD,
		DisableReasoning: config.ReasoningMode == TemporalStructureOpenRouterReasoningDisabled,
		Title:            temporalStructureOpenRouterTitle,
		Reserve: func(requestSHA string) error {
			spent, spendErr := temporalStructureOpenRouterCheckpointSpend(*checkpoint)
			if spendErr != nil {
				return spendErr
			}
			if len(checkpoint.Attempts) >= config.MaxRequests || spent > config.MaxSpendNanoUSD-config.MaxChargeNanoUSD {
				return fmt.Errorf("%w before structure alias %q", errTemporalOpenRouterBudget, item.Alias)
			}
			checkpoint.Attempts = append(checkpoint.Attempts, TemporalStructureOpenRouterAttempt{
				Alias: item.Alias, RequestedAt: now().UTC(), RequestSHA256: requestSHA,
				State: temporalOpenRouterAttemptReserved, ReservedNanoUSD: config.MaxChargeNanoUSD,
			})
			return persistTemporalStructureOpenRouterCheckpoint(config.CheckpointDir, *checkpoint, selected)
		},
	})
	latency := max(int64(0), time.Since(started).Milliseconds())
	call := fillereval.TemporalInferenceCall{
		Axis: "structure", Attempt: 1, ResponseSHA256: callResult.ResponseSHA256,
		LatencyMS: latency, PromptTokens: callResult.Wire.Usage.PromptTokens, CompletionTokens: callResult.Wire.Usage.CompletionTokens,
	}
	if callResult.ResponseSHA256 != "" {
		relative, writeErr := writeTemporalStructureOpenRouterRawResponse(config.CheckpointDir, item.Alias, callResult.RawResponse)
		if writeErr != nil {
			return TemporalStructureAssessment{}, fmt.Errorf("persist raw OpenRouter structure response: %w", writeErr)
		}
		checkpoint.Attempts[len(checkpoint.Attempts)-1].RawResponsePath = relative
	}
	var wire temporalStructureOpenRouterWire
	if callErr == nil {
		if decodeErr := decodeStrictReviewJSON([]byte(callResult.StructuredOutput), &wire); decodeErr != nil {
			callErr = fmt.Errorf("structure assessment JSON is invalid: %w", decodeErr)
		} else {
			normalizeTemporalStructureOpenRouterWire(&wire)
		}
	}
	var failure *temporalCallError
	if callErr != nil {
		failure = classifyTemporalOpenRouterFailure(caseCtx, callResult, callErr)
		call.OperationalFailure = failure.code
	} else if validateErr := validateTemporalStructureOpenRouterWire(wire, item.Video.DurationMS); validateErr != nil {
		failure = &temporalCallError{code: fillereval.TemporalFailureInvalidResponse, detail: validateErr.Error()}
		call.OperationalFailure = failure.code
	}
	if callResult.RequestSHA256 == "" || len(checkpoint.Attempts) == 0 || checkpoint.Attempts[len(checkpoint.Attempts)-1].RequestSHA256 != callResult.RequestSHA256 {
		if errors.Is(callErr, errTemporalOpenRouterBudget) {
			return TemporalStructureAssessment{}, callErr
		}
		return TemporalStructureAssessment{}, fmt.Errorf("OpenRouter structure call for alias %q did not acquire a durable reservation: %w", item.Alias, callErr)
	}
	attempt := &checkpoint.Attempts[len(checkpoint.Attempts)-1]
	attempt.ResponseSHA256, attempt.GenerationID = callResult.ResponseSHA256, callResult.Wire.ID
	attempt.LatencyMS, attempt.PromptTokens, attempt.CompletionTokens = latency, call.PromptTokens, call.CompletionTokens
	if callResult.ChargeKnown {
		attempt.ChargedAmountUSD, attempt.ChargedNanoUSD = callResult.Wire.Usage.Cost.String(), callResult.ChargedNanoUSD
	}
	if failure == nil {
		attempt.State = temporalOpenRouterAttemptAccepted
	} else {
		attempt.OperationalFailure = failure.code
		if callResult.ChargeKnown {
			attempt.State = temporalOpenRouterAttemptFailed
		} else {
			attempt.State = temporalOpenRouterAttemptUnsettled
		}
	}
	if failure != nil {
		return temporalStructureFailedAssessment(item.Alias, now().UTC(), call, failure), nil
	}
	assessment := temporalStructureAssessmentFromWire(item.Alias, wire, now().UTC(), call)
	if err := validateTemporalStructureAssessment(assessment, item.Video.DurationMS, time.Time{}, now().UTC()); err != nil {
		return TemporalStructureAssessment{}, err
	}
	return assessment, nil
}

func temporalStructureFailedAssessment(alias string, assessedAt time.Time, call fillereval.TemporalInferenceCall, failure *temporalCallError) TemporalStructureAssessment {
	if failure == nil {
		failure = &temporalCallError{code: fillereval.TemporalFailureContextExhausted, detail: "structure request was not reserved"}
	}
	return TemporalStructureAssessment{
		Alias:              alias,
		OperationalFailure: &fillereval.TemporalOperationalFailure{Code: failure.code, Detail: boundedTemporalDetail(failure.detail), Retryable: failure.retryable},
		Inference:          temporalInferenceFromCalls(assessedAt, []fillereval.TemporalInferenceCall{call}),
	}
}
