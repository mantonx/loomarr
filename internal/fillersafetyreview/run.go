package fillersafetyreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/httpx"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

// RunOpenRouter performs one independent, serial, checkpointed model review
// and publishes a review only after every case has a decisive assessment.
func RunOpenRouter(ctx context.Context, config Config) (Result, error) {
	client := httpx.NewNamed("filler-spoken-model-review", httpx.TimeoutLLM)
	copy := *client
	copy.Timeout = 0
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return runOpenRouter(ctx, config, reviewRuntime{
		baseURL: fillerbakeoff.OpenRouterBaseURL, client: &copy, now: time.Now,
		call: openroutermedia.Call, extract: ffmpegAudioExtractor{}, identify: identifyFFmpeg,
	})
}

func runOpenRouter(ctx context.Context, config Config, runtime reviewRuntime) (result Result, err error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("spoken-safety model review requires an active context")
	}
	plan, planRaw, err := loadPlan(config)
	if err != nil {
		return Result{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(plan.MaximumWallTimeMS)*time.Millisecond)
	defer cancel()
	loaded, err := loadInputs(runCtx, config, plan, planRaw)
	if err != nil {
		return Result{}, err
	}
	ffmpeg, ffmpegPath, err := validateInputs(runCtx, config, &loaded, runtime)
	if err != nil {
		return Result{}, err
	}
	identity := checkpointIdentity{
		SchemaVersion: checkpointSchemaVersion, PlanSHA256: loaded.planSHA256,
		DraftSHA256: loaded.draftSHA256, WorklistSHA256: loaded.worklistSHA256,
		PolicySHA256: loaded.policySHA256, SnapshotSHA256: loaded.snapshotSHA256,
		ReviewerID: loaded.plan.ReviewerID, ModelFamily: loaded.plan.ModelFamily,
		Model: loaded.plan.Model, ResolvedModel: loaded.plan.ResolvedModel,
		UpstreamProvider: loaded.plan.UpstreamProvider, UpstreamProviderSlug: loaded.plan.UpstreamProviderSlug,
		DisableReasoning: loaded.plan.DisableReasoning,
		PromptSHA256:     promptSHA256(loaded.policy), SchemaSHA256: schemaSHA256(loaded.policy), FFmpeg: ffmpeg,
		ExpectedCases: loaded.plan.ExpectedCases, MaximumRequests: loaded.plan.MaximumRequests,
		MaximumChargeNanoUSD: loaded.plan.MaximumChargeNanoUSD, MaximumSpendNanoUSD: loaded.plan.MaximumSpendNanoUSD,
	}
	if identity.PromptSHA256 == "" || identity.SchemaSHA256 == "" {
		return Result{}, fmt.Errorf("model review prompt or schema identity is invalid")
	}
	if err := ensureCheckpointDirectory(config.CheckpointDirectory); err != nil {
		return Result{}, err
	}
	lock, err := acquireActiveLock(config.CheckpointDirectory)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if releaseErr := lock.release(); releaseErr != nil {
			result = Result{}
			err = errors.Join(err, releaseErr)
		}
	}()
	state, err := loadCheckpoint(config.CheckpointDirectory, identity, runtime.now().UTC())
	if err != nil {
		return Result{}, err
	}
	if err := validateCheckpointAgainstInputs(state, loaded); err != nil {
		return Result{}, err
	}
	if err := rejectUnsettled(state); err != nil {
		return Result{}, err
	}
	if err := rejectPrematureOutput(config.OutputPath, state); err != nil {
		return Result{}, err
	}
	for caseIndex := len(state.Accepted); caseIndex < len(loaded.worklist.Cases); caseIndex++ {
		caseCtx, caseCancel := context.WithTimeout(runCtx, time.Duration(loaded.plan.PerCaseTimeoutMS)*time.Millisecond)
		if err := validateSnapshot(&loaded, runtime.baseURL, runtime.now().UTC()); err != nil {
			caseCancel()
			return Result{}, err
		}
		if err := caseCtx.Err(); err != nil {
			caseCancel()
			return Result{}, fmt.Errorf("model review case %d exceeded its time ceiling: %w", caseIndex+1, err)
		}
		work := loaded.worklist.Cases[caseIndex]
		draftCase := loaded.draft.Cases[caseIndex]
		if rights, ok := loaded.knownScriptRights[work.CaseID]; ok {
			if _, err := authorizeKnownScriptRights(
				loaded.root, rights, runtime.now().UTC(), plannedKnownScriptProcessor(loaded.plan, runtime.baseURL),
			); err != nil {
				caseCancel()
				return Result{}, fmt.Errorf("model review case %d processor authorization changed", caseIndex+1)
			}
		}
		if err := caseCtx.Err(); err != nil {
			caseCancel()
			return Result{}, fmt.Errorf("model review case %d exceeded its time ceiling: %w", caseIndex+1, err)
		}
		sourcePath, err := resolveRootPath(loaded.root, work.SourcePath)
		if err != nil {
			caseCancel()
			return Result{}, fmt.Errorf("model review case %d source path changed", caseIndex+1)
		}
		mediaPlan, err := fillersafety.PlanCompleteMedia(caseCtx, fillersafety.SourceRequest{
			Authority: draftCase.SourceAuthority, Path: sourcePath,
		})
		if err != nil {
			caseCancel()
			return Result{}, fmt.Errorf("model review case %d source bytes changed", caseIndex+1)
		}
		wav, extractErr := runtime.extract.Extract(caseCtx, ffmpegPath, ffmpeg, &mediaPlan, loaded.plan.MaximumAudioBytes)
		closeErr := mediaPlan.Close()
		if extractErr != nil || closeErr != nil {
			caseCancel()
			return Result{}, fmt.Errorf("model review case %d complete audio is invalid", caseIndex+1)
		}
		attemptNumber := nextAttempt(state, work.CaseID)
		observation, assessment, transport, failure, reviewErr := reviewOne(
			caseCtx, runtime, loaded, work, wav, config.APIKey,
			func(requestSHA256 string) error {
				spent, err := checkpointSpend(state)
				if err != nil {
					return err
				}
				if len(state.Attempts) >= loaded.plan.MaximumRequests ||
					spent > loaded.plan.MaximumSpendNanoUSD-loaded.plan.MaximumChargeNanoUSD {
					return fmt.Errorf("model review request or spend reservation is exhausted")
				}
				state.Attempts = append(state.Attempts, attempt{
					CaseID: work.CaseID, Attempt: attemptNumber, RequestedAt: runtime.now().UTC(),
					RequestSHA256: requestSHA256, State: attemptReserved,
					ReservedNanoUSD: loaded.plan.MaximumChargeNanoUSD,
				})
				return persistCheckpoint(config.CheckpointDirectory, state)
			},
		)
		caseCancel()
		reserved := transport.RequestSHA256 != "" && len(state.Attempts) > 0 &&
			state.Attempts[len(state.Attempts)-1].CaseID == work.CaseID &&
			state.Attempts[len(state.Attempts)-1].RequestSHA256 == transport.RequestSHA256
		if !reserved {
			if reviewErr != nil {
				return Result{}, fmt.Errorf("model review case %d failed before reservation: %w", caseIndex+1, reviewErr)
			}
			return Result{}, fmt.Errorf("model review case %d completed without a durable reservation", caseIndex+1)
		}
		current := &state.Attempts[len(state.Attempts)-1]
		current.ResponseSHA256 = transport.ResponseSHA256
		current.GenerationID = transport.GenerationID
		current.PromptTokens = transport.PromptTokens
		current.CompletionTokens = transport.CompletionTokens
		if observation.Verdict != "" {
			current.ObservationSHA256 = observationSHA256(observation)
		}
		if transport.ChargeKnown {
			current.ChargedAmountUSD = transport.ChargedAmountUSD
			current.ChargedNanoUSD = transport.ChargedNanoUSD
			current.State = attemptFailed
		} else {
			current.State = attemptUnsettled
		}
		current.Failure = failure
		if reviewErr != nil {
			if current.Failure == "" {
				current.Failure = failureProvider
			}
			if err := persistCheckpoint(config.CheckpointDirectory, state); err != nil {
				return Result{}, fmt.Errorf("persist failed model review attempt: %w", err)
			}
			return Result{}, fmt.Errorf("model review case %d: %w", caseIndex+1, reviewErr)
		}
		if !transport.ChargeKnown || transport.ResponseSHA256 == "" || transport.GenerationID == "" {
			return Result{}, fmt.Errorf("model review case %d lacks settled transport authority", caseIndex+1)
		}
		current.State = attemptAccepted
		current.Failure = ""
		current.ReviewedAt = runtime.now().UTC()
		state.Accepted = append(state.Accepted, acceptedCase{
			Assessment: assessment, Observation: observation, Attempt: attemptNumber,
		})
		if err := persistCheckpoint(config.CheckpointDirectory, state); err != nil {
			return Result{}, fmt.Errorf("persist accepted model review case: %w", err)
		}
	}
	if state.CompletedAt.IsZero() {
		state.CompletedAt = runtime.now().UTC()
		if err := persistCheckpoint(config.CheckpointDirectory, state); err != nil {
			return Result{}, fmt.Errorf("persist completed model review: %w", err)
		}
	}
	return publishCompletedReview(config.OutputPath, loaded, state, ffmpeg)
}

func rejectUnsettled(value checkpoint) error {
	for _, item := range value.Attempts {
		if item.State == attemptReserved || item.State == attemptUnsettled {
			return fmt.Errorf("model review has an unsettled prior request requiring explicit reconciliation")
		}
	}
	return nil
}

func nextAttempt(value checkpoint, caseID string) int {
	result := 1
	for _, item := range value.Attempts {
		if item.CaseID == caseID {
			result = item.Attempt + 1
		}
	}
	return result
}
