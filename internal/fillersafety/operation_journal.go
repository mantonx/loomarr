package fillersafety

import (
	"context"
	"errors"
	"slices"

	"github.com/loomarr/loomarr/internal/openroutermedia"
)

var errEvaluationBudgetHeld = errors.New("spoken-safety evaluation: hosted-call budget held")

type ledgerCascadeJournal struct {
	repository ExecutionRepository
	run        LedgerRun
	timeline   *evaluationTimeline
	budget     HostedCallBudget
	eventIDs   []string
}

func (j *ledgerCascadeJournal) proposal(ctx context.Context, evidence Evidence) error {
	event := LedgerEvent{
		ID:    evaluationLedgerID(j.run.ID, "proposal", len(j.eventIDs)),
		RunID: j.run.ID, Ordinal: len(j.eventIDs), Kind: LedgerProposalCompleted,
		CreatedAt: j.timeline.next(),
		Proposal: &ProposalCompleted{
			State: evidence.ProposalState, ProposerSHA256: j.run.ProposerSHA256,
			Candidates: slices.Clone(evidence.Candidates),
		},
	}
	if err := j.repository.AppendSpokenSafetyEvent(ctx, event); err != nil {
		return err
	}
	j.eventIDs = append(j.eventIDs, event.ID)
	return nil
}

func (j *ledgerCascadeJournal) audio(
	ctx context.Context,
	adapter audioAdjudicator,
	plan *CompleteMediaPlan,
	candidate Candidate,
	wav []byte,
) (audioAttempt, error) {
	identity := adapter.identity(plan.Audio.EndMS)
	if !validHostedIdentity(identity) {
		return audioAttempt{}, ErrEvaluationInvalid
	}
	startMS := max(int64(0), candidate.StartMS-candidateAudioContextMS)
	endMS := min(plan.Audio.EndMS, candidate.EndMS+candidateAudioContextMS)
	call := hostedCall{
		identity: identity, role: "spoken-safety", rung: "native-audio",
		candidateID: candidate.ID, modalities: []string{"audio"},
		derivativeBytes: int64(len(wav)), derivativeDurationMS: endMS - startMS,
	}
	var reservation LedgerEvent
	reserveErr := error(nil)
	reserve := func(requestSHA256 string) error {
		if reservation.ID != "" || reserveErr != nil {
			reserveErr = ErrEvaluationInvalid
			return reserveErr
		}
		reservation, reserveErr = j.reserve(ctx, plan, call, requestSHA256)
		if reserveErr != nil {
			return reserveErr
		}
		if isBudgetHeld(reservation) {
			return errEvaluationBudgetHeld
		}
		return nil
	}
	attempt, callErr := adapter.adjudicate(ctx, candidate, wav, reserve)
	attempt = normalizedAudioAttempt(candidate, attempt, callErr)
	if reserveErr != nil {
		return audioAttempt{}, reserveErr
	}
	if reservation.ID == "" {
		if callErr == nil {
			return audioAttempt{}, ErrEvaluationInvalid
		}
		return attempt, nil
	}
	if isBudgetHeld(reservation) {
		attempt.Assessment = AudioAssessment{CandidateID: candidate.ID, State: AudioFailed, MatchedRuleIDs: []string{}}
		attempt.MatchedRuleIDs = []string{}
		return attempt, nil
	}
	if !attempt.Transport.ChargeKnown {
		return audioAttempt{}, ErrEvaluationIncomplete
	}
	settled, err := j.settle(ctx, call, reservation, string(attempt.Assessment.State), attempt.Transport, callErr)
	if err != nil {
		return audioAttempt{}, err
	}
	if settled.Settle.Failure == FailureBudget {
		attempt.Assessment = AudioAssessment{CandidateID: candidate.ID, State: AudioFailed, MatchedRuleIDs: []string{}}
		attempt.MatchedRuleIDs = []string{}
	}
	return attempt, nil
}

func (j *ledgerCascadeJournal) video(
	ctx context.Context,
	adapter videoCorroborator,
	plan *CompleteMediaPlan,
) (videoAttempt, error) {
	identity := adapter.identity(plan.Video.EndMS)
	if !validHostedIdentity(identity) {
		return videoAttempt{}, ErrEvaluationInvalid
	}
	call := hostedCall{
		identity: identity, role: "spoken-safety", rung: "complete-video",
		modalities: []string{"audio", "video"}, derivativeBytes: plan.SourceBytes,
		derivativeDurationMS: plan.Video.EndMS,
	}
	var reservation LedgerEvent
	reserveErr := error(nil)
	reserve := func(requestSHA256 string) error {
		if reservation.ID != "" || reserveErr != nil {
			reserveErr = ErrEvaluationInvalid
			return reserveErr
		}
		reservation, reserveErr = j.reserve(ctx, plan, call, requestSHA256)
		if reserveErr != nil {
			return reserveErr
		}
		if isBudgetHeld(reservation) {
			return errEvaluationBudgetHeld
		}
		return nil
	}
	attempt, callErr := adapter.corroborate(ctx, plan, reserve)
	attempt = normalizedVideoAttempt(attempt, callErr)
	if reserveErr != nil {
		return videoAttempt{}, reserveErr
	}
	if reservation.ID == "" {
		if callErr == nil {
			return videoAttempt{}, ErrEvaluationInvalid
		}
		return attempt, nil
	}
	if isBudgetHeld(reservation) {
		attempt.State = VideoFailed
		return attempt, nil
	}
	if !attempt.Transport.ChargeKnown {
		return videoAttempt{}, ErrEvaluationIncomplete
	}
	settled, err := j.settle(ctx, call, reservation, string(attempt.State), attempt.Transport, callErr)
	if err != nil {
		return videoAttempt{}, err
	}
	if settled.Settle.Failure == FailureBudget {
		attempt.State = VideoFailed
	}
	return attempt, nil
}

type hostedCall struct {
	identity                                      hostedCallIdentity
	role, rung, candidateID                       string
	modalities                                    []string
	derivativeBytes, derivativeDurationMS, pixels int64
}

func (j *ledgerCascadeJournal) reserve(
	ctx context.Context,
	plan *CompleteMediaPlan,
	call hostedCall,
	requestSHA256 string,
) (LedgerEvent, error) {
	ordinal := len(j.eventIDs)
	command := HostedCallReservation{
		EventID: evaluationLedgerID(j.run.ID, "reserve", ordinal), RunID: j.run.ID,
		EvaluationID: evaluationLedgerID(j.run.ID, "inference", ordinal), ClipHash: j.run.ClipHash,
		CandidateID: call.candidateID, RequestSHA256: requestSHA256,
		Role: call.role, Rung: call.rung, RequestedProvider: call.identity.RequestedProvider,
		RequestedModel: call.identity.RequestedModel, UpstreamProvider: call.identity.UpstreamProvider,
		Modalities: slices.Clone(call.modalities), DerivativeBytes: call.derivativeBytes,
		DerivativeDurationMS: call.derivativeDurationMS, DerivativePixels: call.pixels,
		RequestedNanoUSD: call.identity.MaxChargeNanoUSD, Budget: j.budget,
		Versions: HostedCallVersions{
			EvidenceSHA256: plan.AuthoritySHA256, ExtractorSHA256: plan.FFmpeg.BinarySHA256,
			PromptSHA256: call.identity.PromptSHA256, SchemaSHA256: call.identity.SchemaSHA256,
			TaxonomySHA256: plan.PolicySHA256, CertificationSHA256: j.run.CertificationSHA256,
			PolicySHA256: plan.PolicySHA256, CapabilitySHA256: call.identity.CapabilitySHA256,
		},
		Ordinal: ordinal, CreatedAt: j.timeline.next(),
	}
	event, err := j.repository.ReserveSpokenSafetyCall(ctx, command)
	if err != nil {
		return LedgerEvent{}, err
	}
	if err := validateReservationReceipt(command, event); err != nil {
		return LedgerEvent{}, err
	}
	j.eventIDs = append(j.eventIDs, event.ID)
	return event, nil
}

func (j *ledgerCascadeJournal) settle(
	ctx context.Context,
	call hostedCall,
	reservation LedgerEvent,
	outcome string,
	transport openroutermedia.Result,
	callErr error,
) (LedgerEvent, error) {
	ordinal := len(j.eventIDs)
	failure := closedFailure(outcome, callErr)
	resolvedProvider, resolvedModel, upstreamProvider := call.identity.ResolvedProvider, call.identity.ResolvedModel, call.identity.UpstreamProvider
	if failure != FailureNone {
		outcome = ""
	}
	if failure == FailureTransport || failure == FailureRouteMismatch {
		resolvedProvider, resolvedModel, upstreamProvider = "", "", ""
	}
	command := HostedCallSettlement{
		EventID: evaluationLedgerID(j.run.ID, "settle", ordinal), RunID: j.run.ID,
		ReservationEventID: reservation.ID, ResponseSHA256: transport.ResponseSHA256,
		ResolvedProvider: resolvedProvider, ResolvedModel: resolvedModel,
		UpstreamProvider: upstreamProvider, GenerationID: transport.GenerationID,
		Outcome: outcome, Failure: failure, ChargedAmountUSD: transport.ChargedAmountUSD,
		ChargedNanoUSD: transport.ChargedNanoUSD, ChargeKnown: transport.ChargeKnown,
		PromptTokens: transport.PromptTokens, CompletionTokens: transport.CompletionTokens,
		Ordinal: ordinal, CreatedAt: j.timeline.next(),
	}
	event, err := j.repository.SettleSpokenSafetyCall(ctx, command)
	if err != nil {
		return LedgerEvent{}, err
	}
	if err := validateSettlementReceipt(command, event); err != nil {
		return LedgerEvent{}, err
	}
	j.eventIDs = append(j.eventIDs, event.ID)
	return event, nil
}

func validateReservationReceipt(command HostedCallReservation, event LedgerEvent) error {
	if event.ID != command.EventID || event.RunID != command.RunID || event.Ordinal != command.Ordinal ||
		event.Kind != LedgerInferenceReserved || !event.CreatedAt.Equal(command.CreatedAt) || event.Reserve == nil {
		return ErrEvaluationInvalid
	}
	reservation := event.Reserve
	if reservation.EvaluationID != command.EvaluationID || reservation.RequestSHA256 != command.RequestSHA256 ||
		reservation.RequestedProvider != command.RequestedProvider || reservation.RequestedModel != command.RequestedModel ||
		reservation.UpstreamProvider != command.UpstreamProvider || reservation.CapabilitySHA256 != command.Versions.CapabilitySHA256 ||
		reservation.PromptSHA256 != command.Versions.PromptSHA256 || reservation.SchemaSHA256 != command.Versions.SchemaSHA256 ||
		reservation.CandidateID != command.CandidateID ||
		!slices.Equal(reservation.Modalities, command.Modalities) || reservation.RequestedNanoUSD != command.RequestedNanoUSD {
		return ErrEvaluationInvalid
	}
	if _, err := CanonicalLedgerEvent(event); err != nil {
		return ErrEvaluationInvalid
	}
	return nil
}

func validateSettlementReceipt(command HostedCallSettlement, event LedgerEvent) error {
	if event.ID != command.EventID || event.RunID != command.RunID || event.Ordinal != command.Ordinal ||
		event.Kind != LedgerInferenceSettled || !event.CreatedAt.Equal(command.CreatedAt) || event.Settle == nil {
		return ErrEvaluationInvalid
	}
	settlement := event.Settle
	if settlement.ReservationEventID != command.ReservationEventID ||
		settlement.ResponseSHA256 != command.ResponseSHA256 || settlement.ResolvedProvider != command.ResolvedProvider ||
		settlement.ResolvedModel != command.ResolvedModel || settlement.UpstreamProvider != command.UpstreamProvider ||
		settlement.GenerationID != command.GenerationID || settlement.ChargeKnown != command.ChargeKnown ||
		settlement.ChargedAmountUSD != command.ChargedAmountUSD || settlement.ChargedNanoUSD != command.ChargedNanoUSD ||
		settlement.PromptTokens != command.PromptTokens || settlement.CompletionTokens != command.CompletionTokens {
		return ErrEvaluationInvalid
	}
	if command.Failure == FailureNone {
		if settlement.State != SettlementCompleted && settlement.Failure != FailureBudget ||
			settlement.State == SettlementCompleted && settlement.Outcome != command.Outcome {
			return ErrEvaluationInvalid
		}
	} else if settlement.State != SettlementFailed || settlement.Failure != command.Failure || settlement.Outcome != "" {
		return ErrEvaluationInvalid
	}
	if _, err := CanonicalLedgerEvent(event); err != nil {
		return ErrEvaluationInvalid
	}
	return nil
}
