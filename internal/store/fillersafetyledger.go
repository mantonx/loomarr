package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

// SpokenSafetyInferenceReservation is the caller-owned identity of one
// pre-request ledger event. The store supplies its budget disposition from the
// inference reservation committed in the same transaction.
type SpokenSafetyInferenceReservation struct {
	EventID, RunID, CandidateID, RequestSHA256 string
	Ordinal                                    int
	CreatedAt                                  time.Time
}

// SpokenSafetyInferenceSettlement supplies only closed ledger facts that are
// not already authority-owned by the V62 settlement.
type SpokenSafetyInferenceSettlement struct {
	EventID, RunID, ReservationEventID, ResponseSHA256 string
	Failure                                            fillersafety.SettlementFailure
	ChargeKnown                                        bool
	Ordinal                                            int
	CreatedAt                                          time.Time
}

const spokenSafetyRunSelect = `SELECT id, clip_hash, authority_sha256, source_sha256,
	source_bytes, duration_ms, certification_sha256, policy_sha256, proposer_sha256, implementation, created_at
	FROM filler_spoken_safety_runs`

const spokenSafetyEventSelect = `SELECT id, run_id, ordinal, kind, payload_json, created_at
	FROM filler_spoken_safety_events`

func (s *sqlStore) PutSpokenSafetyRun(ctx context.Context, run fillersafety.LedgerRun) error {
	_, err := s.BeginSpokenSafetyRun(ctx, run)
	return err
}

func (s *sqlStore) BeginSpokenSafetyRun(ctx context.Context, run fillersafety.LedgerRun) (bool, error) {
	if err := fillersafety.ValidateLedgerRun(run); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, s.ph(`INSERT INTO filler_spoken_safety_runs (
		id, clip_hash, authority_sha256, source_sha256, source_bytes, duration_ms, certification_sha256,
		policy_sha256, proposer_sha256, implementation, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`),
		run.ID, run.ClipHash, run.AuthoritySHA256, run.SourceSHA256, run.SourceBytes, run.DurationMS,
		run.CertificationSHA256, run.PolicySHA256, run.ProposerSHA256, run.Implementation, fillerDecisionEpoch(run.CreatedAt))
	if err != nil {
		return false, fmt.Errorf("insert spoken-safety run: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect spoken-safety run insert: %w", err)
	}
	if inserted != 0 {
		return true, nil
	}
	existing, err := scanSpokenSafetyRun(s.db.QueryRowContext(ctx,
		s.ph(spokenSafetyRunSelect+` WHERE id = ?`), run.ID))
	if err != nil {
		return false, fmt.Errorf("read existing spoken-safety run: %w", err)
	}
	if existing != run {
		return false, fillersafety.ErrLedgerConflict
	}
	return false, nil
}

func (s *sqlStore) GetSpokenSafetyRun(ctx context.Context, id string) (fillersafety.LedgerRun, error) {
	run, err := scanSpokenSafetyRun(s.db.QueryRowContext(ctx,
		s.ph(spokenSafetyRunSelect+` WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return fillersafety.LedgerRun{}, ErrNotFound
	}
	if err != nil {
		return fillersafety.LedgerRun{}, fmt.Errorf("get spoken-safety run: %w", err)
	}
	return run, nil
}

func scanSpokenSafetyRun(row scannable) (fillersafety.LedgerRun, error) {
	var run fillersafety.LedgerRun
	var createdAt int64
	if err := row.Scan(&run.ID, &run.ClipHash, &run.AuthoritySHA256, &run.SourceSHA256,
		&run.SourceBytes, &run.DurationMS, &run.CertificationSHA256, &run.PolicySHA256, &run.ProposerSHA256, &run.Implementation,
		&createdAt); err != nil {
		return fillersafety.LedgerRun{}, err
	}
	run.CreatedAt = fromFillerDecisionEpoch(createdAt)
	return run, nil
}

func (s *sqlStore) AppendSpokenSafetyEvent(ctx context.Context, event fillersafety.LedgerEvent) error {
	if event.Kind == fillersafety.LedgerInferenceReserved || event.Kind == fillersafety.LedgerInferenceSettled {
		return fillersafety.ErrLedgerInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin spoken-safety event: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.appendSpokenSafetyEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit spoken-safety event: %w", err)
	}
	return nil
}

func (s *sqlStore) ReserveSpokenSafetyInference(
	ctx context.Context,
	command SpokenSafetyInferenceReservation,
	evaluation InferenceEvaluation,
	budget InferenceBudget,
) (InferenceEvaluation, fillersafety.LedgerEvent, error) {
	if command.EventID == "" || command.RunID == "" || command.Ordinal < 0 || command.CreatedAt.IsZero() ||
		command.RequestSHA256 == "" || evaluation.RunID != command.RunID {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerInvalid
	}
	if err := validateInferenceReservation(evaluation, budget); err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	}
	evaluation.Modalities = slices.Clone(evaluation.Modalities)
	slices.Sort(evaluation.Modalities)
	requestedNanoUSD := evaluation.ReservedNanoUSD
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fmt.Errorf("begin spoken-safety inference reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if existing, found, err := getSpokenSafetyEvent(ctx, tx, s.ph, command.EventID); err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	} else if found {
		stored, err := scanInferenceEvaluation(tx.QueryRowContext(ctx,
			s.ph(inferenceEvaluationSelect+` WHERE id = ?`), fillersafety.LedgerEventInferenceID(existing)))
		if err != nil || !sameSpokenSafetyReservationCommand(existing, command, evaluation, requestedNanoUSD) {
			return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerConflict
		}
		return stored, existing, nil
	}

	run, err := scanSpokenSafetyRun(tx.QueryRowContext(ctx,
		s.ph(spokenSafetyRunSelect+` WHERE id = ?`), command.RunID))
	if errors.Is(err, sql.ErrNoRows) {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, ErrNotFound
	}
	if err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fmt.Errorf("read spoken-safety reservation run: %w", err)
	}
	if evaluation.ClipHash != run.ClipHash || !evaluation.CreatedAt.Equal(command.CreatedAt) {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerInvalid
	}
	evaluation, err = s.reserveInferenceEvaluation(ctx, tx, evaluation, budget)
	if err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	}
	state := fillersafety.ReservationAccepted
	if evaluation.State == InferenceHeldBudget {
		state = fillersafety.ReservationHeldBudget
	}
	event := fillersafety.LedgerEvent{
		ID: command.EventID, RunID: command.RunID, Ordinal: command.Ordinal,
		Kind: fillersafety.LedgerInferenceReserved, CreatedAt: command.CreatedAt,
		Reserve: &fillersafety.InferenceReserved{
			EvaluationID: evaluation.ID, RequestSHA256: command.RequestSHA256,
			RequestedProvider: evaluation.RequestedProvider, RequestedModel: evaluation.RequestedModel,
			UpstreamProvider: evaluation.UpstreamProvider, CapabilitySHA256: evaluation.Versions.CapabilitySnapshot,
			PromptSHA256: evaluation.Versions.Prompt, SchemaSHA256: evaluation.Versions.Schema,
			CandidateID: command.CandidateID,
			Modalities:  evaluation.Modalities, RequestedNanoUSD: requestedNanoUSD,
			ReservedNanoUSD: evaluation.ReservedNanoUSD, State: state,
		},
	}
	if err := s.appendSpokenSafetyEvent(ctx, tx, event); err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fmt.Errorf("commit spoken-safety inference reservation: %w", err)
	}
	return evaluation, event, nil
}

func sameSpokenSafetyReservationCommand(
	event fillersafety.LedgerEvent,
	command SpokenSafetyInferenceReservation,
	evaluation InferenceEvaluation,
	requestedNanoUSD int64,
) bool {
	reservation := event.Reserve
	return reservation != nil && event.ID == command.EventID && event.RunID == command.RunID &&
		event.Ordinal == command.Ordinal && event.CreatedAt.Equal(command.CreatedAt) &&
		reservation.EvaluationID == evaluation.ID && reservation.RequestSHA256 == command.RequestSHA256 &&
		reservation.RequestedProvider == evaluation.RequestedProvider && reservation.RequestedModel == evaluation.RequestedModel &&
		reservation.UpstreamProvider == evaluation.UpstreamProvider && reservation.CapabilitySHA256 == evaluation.Versions.CapabilitySnapshot &&
		reservation.PromptSHA256 == evaluation.Versions.Prompt && reservation.SchemaSHA256 == evaluation.Versions.Schema &&
		reservation.CandidateID == command.CandidateID &&
		slices.Equal(reservation.Modalities, evaluation.Modalities) && reservation.RequestedNanoUSD == requestedNanoUSD
}

func (s *sqlStore) SettleSpokenSafetyInference(
	ctx context.Context,
	command SpokenSafetyInferenceSettlement,
	settlement InferenceSettlement,
) (InferenceEvaluation, fillersafety.LedgerEvent, error) {
	if command.EventID == "" || command.RunID == "" || command.ReservationEventID == "" ||
		command.Ordinal < 0 || command.CreatedAt.IsZero() || !settlement.UpdatedAt.Equal(command.CreatedAt) {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerInvalid
	}
	if command.Failure == fillersafety.FailureNone && settlement.State != "" && settlement.State != InferenceCompleted ||
		command.Failure != fillersafety.FailureNone && settlement.State != InferenceFailed {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerInvalid
	}
	if command.Failure == fillersafety.FailureInterrupted && !settlement.RetainReservation ||
		command.Failure != fillersafety.FailureInterrupted && settlement.RetainReservation {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fmt.Errorf("begin spoken-safety inference settlement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if existing, found, err := getSpokenSafetyEvent(ctx, tx, s.ph, command.EventID); err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	} else if found {
		stored, err := scanInferenceEvaluation(tx.QueryRowContext(ctx,
			s.ph(inferenceEvaluationSelect+` WHERE id = ?`), fillersafety.LedgerEventInferenceID(existing)))
		if err != nil || !sameSpokenSafetySettlementCommand(existing, command, settlement) {
			return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerConflict
		}
		return stored, existing, nil
	}

	reservation, found, err := getSpokenSafetyEvent(ctx, tx, s.ph, command.ReservationEventID)
	if err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	}
	if !found || reservation.RunID != command.RunID || reservation.Reserve == nil ||
		reservation.Reserve.State != fillersafety.ReservationAccepted {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fillersafety.ErrLedgerConflict
	}
	stored, overBudget, err := s.settleInferenceEvaluation(ctx, tx, reservation.Reserve.EvaluationID, settlement)
	if err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	}
	state := fillersafety.SettlementCompleted
	failure := command.Failure
	if failure == fillersafety.FailureInterrupted {
		state = fillersafety.SettlementUnknown
	} else if stored.State != InferenceCompleted || failure != fillersafety.FailureNone {
		state = fillersafety.SettlementFailed
	}
	if overBudget {
		state, failure = fillersafety.SettlementFailed, fillersafety.FailureBudget
	}
	outcome := stored.Outcome
	if state != fillersafety.SettlementCompleted {
		outcome = ""
	}
	event := fillersafety.LedgerEvent{
		ID: command.EventID, RunID: command.RunID, Ordinal: command.Ordinal,
		Kind: fillersafety.LedgerInferenceSettled, CreatedAt: command.CreatedAt,
		Settle: &fillersafety.InferenceSettled{
			ReservationEventID: command.ReservationEventID, EvaluationID: stored.ID,
			ResponseSHA256: command.ResponseSHA256, ResolvedProvider: stored.ResolvedProvider,
			ResolvedModel: stored.ResolvedModel, UpstreamProvider: stored.UpstreamProvider,
			GenerationID: stored.GenerationID, State: state, Failure: failure, Outcome: outcome,
			ChargedAmountUSD: stored.ChargedAmount, ChargedNanoUSD: stored.ChargedNanoUSD,
			AccountedNanoUSD: stored.ReservedNanoUSD,
			ChargeKnown:      command.ChargeKnown, PromptTokens: stored.Tokens.Prompt,
			CompletionTokens: stored.Tokens.Completion,
		},
	}
	if err := s.appendSpokenSafetyEvent(ctx, tx, event); err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return InferenceEvaluation{}, fillersafety.LedgerEvent{}, fmt.Errorf("commit spoken-safety inference settlement: %w", err)
	}
	if overBudget {
		return stored, event, ErrInferenceBudgetExceeded
	}
	return stored, event, nil
}

func sameSpokenSafetySettlementCommand(
	event fillersafety.LedgerEvent,
	command SpokenSafetyInferenceSettlement,
	settlement InferenceSettlement,
) bool {
	result := event.Settle
	failureMatches := result != nil && (result.Failure == command.Failure ||
		result.Failure == fillersafety.FailureBudget && command.Failure == fillersafety.FailureNone)
	if result == nil || !failureMatches || event.ID != command.EventID || event.RunID != command.RunID || event.Ordinal != command.Ordinal ||
		!event.CreatedAt.Equal(command.CreatedAt) || result.ReservationEventID != command.ReservationEventID ||
		result.ResponseSHA256 != command.ResponseSHA256 ||
		result.ChargeKnown != command.ChargeKnown {
		return false
	}
	return result.ResolvedProvider == settlement.ResolvedProvider && result.ResolvedModel == settlement.ResolvedModel &&
		result.UpstreamProvider == settlement.UpstreamProvider && result.GenerationID == settlement.GenerationID &&
		result.ChargedAmountUSD == settlement.ChargedAmount && result.ChargedNanoUSD == settlement.ChargedNanoUSD &&
		result.PromptTokens == settlement.Tokens.Prompt && result.CompletionTokens == settlement.Tokens.Completion
}
