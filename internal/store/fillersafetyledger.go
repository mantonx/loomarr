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
	if err := fillersafety.ValidateLedgerRun(run); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, s.ph(`INSERT INTO filler_spoken_safety_runs (
		id, clip_hash, authority_sha256, source_sha256, source_bytes, duration_ms, certification_sha256,
		policy_sha256, proposer_sha256, implementation, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`),
		run.ID, run.ClipHash, run.AuthoritySHA256, run.SourceSHA256, run.SourceBytes, run.DurationMS,
		run.CertificationSHA256, run.PolicySHA256, run.ProposerSHA256, run.Implementation, fillerDecisionEpoch(run.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert spoken-safety run: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect spoken-safety run insert: %w", err)
	}
	if inserted != 0 {
		return nil
	}
	existing, err := scanSpokenSafetyRun(s.db.QueryRowContext(ctx,
		s.ph(spokenSafetyRunSelect+` WHERE id = ?`), run.ID))
	if err != nil {
		return fmt.Errorf("read existing spoken-safety run: %w", err)
	}
	if existing != run {
		return fillersafety.ErrLedgerConflict
	}
	return nil
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
			PromptSHA256: evaluation.Versions.Prompt, CandidateID: command.CandidateID,
			Modalities: evaluation.Modalities, RequestedNanoUSD: requestedNanoUSD,
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
		reservation.PromptSHA256 == evaluation.Versions.Prompt && reservation.CandidateID == command.CandidateID &&
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

func (s *sqlStore) appendSpokenSafetyEvent(ctx context.Context, tx *sql.Tx, event fillersafety.LedgerEvent) error {
	payload, err := fillersafety.CanonicalLedgerEvent(event)
	if err != nil {
		return err
	}

	existing, found, err := getSpokenSafetyEvent(ctx, tx, s.ph, event.ID)
	if err != nil {
		return err
	}
	if found {
		existingPayload, encodeErr := fillersafety.CanonicalLedgerEvent(existing)
		if encodeErr == nil && sameSpokenSafetyEvent(existing, event, existingPayload, payload) {
			return nil
		}
		return fillersafety.ErrLedgerConflict
	}

	prior, err := listSpokenSafetyEvents(ctx, tx, s.ph, event.RunID)
	if err != nil {
		return err
	}
	if event.Ordinal != len(prior) || !validSpokenSafetyAppend(prior, event) {
		return fillersafety.ErrLedgerConflict
	}

	var inferenceID any
	if id := fillersafety.LedgerEventInferenceID(event); id != "" {
		inferenceID = id
	}
	if _, err := tx.ExecContext(ctx, s.ph(`INSERT INTO filler_spoken_safety_events (
		id, run_id, ordinal, kind, inference_id, payload_json, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`), event.ID, event.RunID, event.Ordinal, event.Kind,
		inferenceID, string(payload), fillerDecisionEpoch(event.CreatedAt)); err != nil {
		return fmt.Errorf("insert spoken-safety event: %w", err)
	}
	return nil
}

func (s *sqlStore) ListSpokenSafetyEvents(ctx context.Context, runID string) ([]fillersafety.LedgerEvent, error) {
	if runID == "" {
		return nil, fillersafety.ErrLedgerInvalid
	}
	return listSpokenSafetyEvents(ctx, s.db, s.ph, runID)
}

type spokenSafetyQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getSpokenSafetyEvent(ctx context.Context, q spokenSafetyQueryer, ph placeholder, id string) (fillersafety.LedgerEvent, bool, error) {
	event, err := scanSpokenSafetyEvent(q.QueryRowContext(ctx,
		ph(spokenSafetyEventSelect+` WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return fillersafety.LedgerEvent{}, false, nil
	}
	if err != nil {
		return fillersafety.LedgerEvent{}, false, fmt.Errorf("read spoken-safety event: %w", err)
	}
	return event, true, nil
}

func listSpokenSafetyEvents(ctx context.Context, q spokenSafetyQueryer, ph placeholder, runID string) ([]fillersafety.LedgerEvent, error) {
	rows, err := q.QueryContext(ctx, ph(spokenSafetyEventSelect+` WHERE run_id = ? ORDER BY ordinal ASC`), runID)
	if err != nil {
		return nil, fmt.Errorf("list spoken-safety events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events := []fillersafety.LedgerEvent{}
	for rows.Next() {
		event, err := scanSpokenSafetyEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan spoken-safety event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate spoken-safety events: %w", err)
	}
	return events, nil
}

func scanSpokenSafetyEvent(row scannable) (fillersafety.LedgerEvent, error) {
	var event fillersafety.LedgerEvent
	var kind string
	var payload string
	var createdAt int64
	if err := row.Scan(&event.ID, &event.RunID, &event.Ordinal, &kind, &payload, &createdAt); err != nil {
		return fillersafety.LedgerEvent{}, err
	}
	decoded, err := fillersafety.DecodeLedgerEvent(fillersafety.LedgerEventKind(kind), []byte(payload))
	if err != nil {
		return fillersafety.LedgerEvent{}, err
	}
	decoded.ID, decoded.RunID, decoded.Ordinal = event.ID, event.RunID, event.Ordinal
	decoded.CreatedAt = fromFillerDecisionEpoch(createdAt)
	if _, err := fillersafety.CanonicalLedgerEvent(decoded); err != nil {
		return fillersafety.LedgerEvent{}, err
	}
	return decoded, nil
}

func sameSpokenSafetyEvent(existing, proposed fillersafety.LedgerEvent, existingPayload, proposedPayload []byte) bool {
	return existing.ID == proposed.ID && existing.RunID == proposed.RunID &&
		existing.Ordinal == proposed.Ordinal && existing.Kind == proposed.Kind &&
		fillerDecisionEpoch(existing.CreatedAt) == fillerDecisionEpoch(proposed.CreatedAt) &&
		slices.Equal(existingPayload, proposedPayload)
}

func validSpokenSafetyAppend(prior []fillersafety.LedgerEvent, event fillersafety.LedgerEvent) bool {
	if len(prior) == 0 {
		return event.Kind == fillersafety.LedgerSourcePlanned
	}
	if prior[len(prior)-1].Kind == fillersafety.LedgerTerminal {
		return false
	}
	if len(prior) == 1 {
		return event.Kind == fillersafety.LedgerProposalCompleted
	}
	if event.Kind == fillersafety.LedgerSourcePlanned || event.Kind == fillersafety.LedgerProposalCompleted {
		return false
	}
	if event.Settle != nil {
		reservationFound := false
		for _, earlier := range prior {
			if earlier.ID == event.Settle.ReservationEventID && earlier.Reserve != nil &&
				earlier.Reserve.EvaluationID == event.Settle.EvaluationID {
				reservationFound = true
			}
			if earlier.Settle != nil && earlier.Settle.ReservationEventID == event.Settle.ReservationEventID {
				return false
			}
		}
		return reservationFound
	}
	if event.Terminal != nil {
		ids := make([]string, 0, len(prior))
		unsettled := map[string]struct{}{}
		for _, earlier := range prior {
			ids = append(ids, earlier.ID)
			if earlier.Reserve != nil && earlier.Reserve.State == fillersafety.ReservationAccepted {
				unsettled[earlier.ID] = struct{}{}
			}
			if earlier.Settle != nil {
				if _, exists := unsettled[earlier.Settle.ReservationEventID]; !exists {
					return false
				}
				delete(unsettled, earlier.Settle.ReservationEventID)
			}
		}
		return len(unsettled) == 0 && slices.Equal(ids, event.Terminal.EventIDs)
	}
	return event.Kind == fillersafety.LedgerInferenceReserved
}
