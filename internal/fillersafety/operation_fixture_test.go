package fillersafety

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/openroutermedia"
	"github.com/loomarr/loomarr/internal/testkit/operationfixture"
	"github.com/loomarr/loomarr/internal/testkit/recordfixture"
)

type operationRepositoryState struct {
	budgetHeld bool
	reserveErr error
	settleErr  error
}

type operationAudioResult struct {
	state         AudioState
	err           error
	unknownCharge bool
}

type operationVideoResult struct {
	state VideoState
	err   error
}

type recordedExecutionRepository = operationfixture.Repository[LedgerRun, LedgerEvent, HostedCallReservation, HostedCallSettlement]

type operationFixture struct {
	request     EvaluationRequest
	repository  *recordedExecutionRepository
	state       *operationRepositoryState
	proposer    *recordfixture.Recorder[proposalRequest, proposalOutput]
	audio       *recordfixture.Recorder[Candidate, audioAttempt]
	video       *recordfixture.Recorder[*CompleteMediaPlan, videoAttempt]
	audioResult *operationAudioResult
	videoResult *operationVideoResult
	operation   *evaluationOperation
}

func newOperationFixture(t *testing.T, intervals []proposedInterval) operationFixture {
	t.Helper()
	contents := []byte("complete operation source")
	path := filepath.Join(t.TempDir(), "opaque-source.mp4")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	authority := validSourceAuthority()
	authority.SourceID = "opaque-source-1"
	authority.SourceSHA256, authority.SourceBytes = sourceIdentity(contents)
	identity := validProposerIdentityFixture()
	state := &operationRepositoryState{}
	repository := recordedRepository(state)
	proposer := proposalRecorder(identity, intervals)
	audioResult := &operationAudioResult{state: AudioAbsent}
	audio := &recordfixture.Recorder[Candidate, audioAttempt]{Respond: func(candidate Candidate) (audioAttempt, error) {
		return audioAttempt{
			Assessment: AudioAssessment{CandidateID: candidate.ID, State: audioResult.state}, MatchedRuleIDs: []string{},
			Transport: hostedTransportFixture("audio", audioResult.unknownCharge),
		}, audioResult.err
	}}
	videoResult := &operationVideoResult{state: VideoNoSignal}
	video := &recordfixture.Recorder[*CompleteMediaPlan, videoAttempt]{Respond: func(*CompleteMediaPlan) (videoAttempt, error) {
		return videoAttempt{State: videoResult.state, Flags: []videoFlag{}, Transport: hostedTransportFixture("video", false)}, videoResult.err
	}}
	nowAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { nowAt = nowAt.Add(time.Millisecond); return nowAt }
	operation, err := newEvaluationOperation(repository, evaluator{
		proposer: func(_ context.Context, request proposalRequest) (proposalOutput, error) {
			return proposer.Call(request)
		}, proposerIdentity: identity,
		audioExtractor: func(context.Context, *CompleteMediaPlan, Candidate) ([]byte, error) { return validCandidateWAV(), nil },
		audio: audioAdjudicatorFuncs{
			identify: func(int64) hostedCallIdentity { return validHostedCallIdentityFixture() },
			invoke: func(_ context.Context, candidate Candidate, _ []byte, reserve func(string) error) (audioAttempt, error) {
				if err := reserve(strings.Repeat("d", 64)); err != nil {
					return audioAttempt{Assessment: AudioAssessment{CandidateID: candidate.ID, State: AudioFailed}, MatchedRuleIDs: []string{}}, err
				}
				return audio.Call(candidate)
			},
		},
		video: videoCorroboratorFuncs{
			identify: func(int64) hostedCallIdentity { return validHostedCallIdentityFixture() },
			invoke: func(_ context.Context, plan *CompleteMediaPlan, reserve func(string) error) (videoAttempt, error) {
				if err := reserve(strings.Repeat("e", 64)); err != nil {
					return videoAttempt{State: VideoFailed, Flags: []videoFlag{}}, err
				}
				return video.Call(plan)
			},
		},
	}, HostedCallBudget{PerClipNanoUSD: 1_000, PerDayNanoUSD: 1_000, PerRunNanoUSD: 1_000}, now)
	if err != nil {
		t.Fatal(err)
	}
	return operationFixture{
		request:    EvaluationRequest{RunID: "spoken-run-1", StartedAt: nowAt.Add(-time.Second), Source: SourceRequest{Authority: authority, Path: path}},
		repository: repository, state: state, proposer: proposer, audio: audio, video: video,
		audioResult: audioResult, videoResult: videoResult, operation: operation,
	}
}

func recordedRepository(state *operationRepositoryState) *recordedExecutionRepository {
	var repository *recordedExecutionRepository
	repository = &recordedExecutionRepository{
		State:         &operationfixture.State[LedgerRun, LedgerEvent]{},
		ValidateRun:   ValidateLedgerRun,
		ConflictError: ErrLedgerConflict,
		ValidateEvent: func(event LedgerEvent) error {
			_, err := CanonicalLedgerEvent(event)
			return err
		},
		ReserveFunc: func(ctx context.Context, command HostedCallReservation) (LedgerEvent, error) {
			if state.reserveErr != nil {
				return LedgerEvent{}, state.reserveErr
			}
			reservationState, reserved := ReservationAccepted, command.RequestedNanoUSD
			if state.budgetHeld {
				reservationState, reserved = ReservationHeldBudget, 0
			}
			event := LedgerEvent{ID: command.EventID, RunID: command.RunID, Ordinal: command.Ordinal, Kind: LedgerInferenceReserved, CreatedAt: command.CreatedAt,
				Reserve: &InferenceReserved{EvaluationID: command.EvaluationID, RequestSHA256: command.RequestSHA256, RequestedProvider: command.RequestedProvider, RequestedModel: command.RequestedModel, UpstreamProvider: command.UpstreamProvider, CapabilitySHA256: command.Versions.CapabilitySHA256, PromptSHA256: command.Versions.PromptSHA256, CandidateID: command.CandidateID, Modalities: slices.Clone(command.Modalities), RequestedNanoUSD: command.RequestedNanoUSD, ReservedNanoUSD: reserved, State: reservationState}}
			return event, nil
		},
		SettleFunc: func(ctx context.Context, command HostedCallSettlement) (LedgerEvent, error) {
			if state.settleErr != nil {
				return LedgerEvent{}, state.settleErr
			}
			events := repository.Events()
			reservation := events[len(events)-1]
			settlementState := SettlementCompleted
			if command.Failure != FailureNone {
				settlementState = SettlementFailed
			}
			event := LedgerEvent{ID: command.EventID, RunID: command.RunID, Ordinal: command.Ordinal, Kind: LedgerInferenceSettled, CreatedAt: command.CreatedAt,
				Settle: &InferenceSettled{ReservationEventID: command.ReservationEventID, EvaluationID: reservation.Reserve.EvaluationID, ResponseSHA256: command.ResponseSHA256, ResolvedProvider: command.ResolvedProvider, ResolvedModel: command.ResolvedModel, UpstreamProvider: command.UpstreamProvider, GenerationID: command.GenerationID, State: settlementState, Failure: command.Failure, Outcome: command.Outcome, ChargedAmountUSD: command.ChargedAmountUSD, ChargedNanoUSD: command.ChargedNanoUSD, AccountedNanoUSD: command.ChargedNanoUSD, ChargeKnown: command.ChargeKnown, PromptTokens: command.PromptTokens, CompletionTokens: command.CompletionTokens}}
			return event, nil
		},
	}
	return repository
}

func hostedTransportFixture(kind string, unknown bool) openroutermedia.Result {
	result := openroutermedia.Result{GenerationID: "generation-" + kind, ResponseSHA256: strings.Repeat("f", 64), PromptTokens: 10, CompletionTokens: 2, ChargedAmountUSD: "0.00000005", ChargedNanoUSD: 50, ChargeKnown: true}
	if unknown {
		result.ChargedAmountUSD, result.ChargedNanoUSD, result.ChargeKnown = "", 0, false
	}
	return result
}
