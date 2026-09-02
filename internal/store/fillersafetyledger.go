package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

const spokenSafetyRunSelect = `SELECT id, clip_hash, authority_sha256, source_sha256,
	source_bytes, certification_sha256, policy_sha256, implementation, created_at
	FROM filler_spoken_safety_runs`

const spokenSafetyEventSelect = `SELECT id, run_id, ordinal, kind, payload_json, created_at
	FROM filler_spoken_safety_events`

func (s *sqlStore) PutSpokenSafetyRun(ctx context.Context, run fillersafety.LedgerRun) error {
	if err := fillersafety.ValidateLedgerRun(run); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, s.ph(`INSERT INTO filler_spoken_safety_runs (
		id, clip_hash, authority_sha256, source_sha256, source_bytes, certification_sha256,
		policy_sha256, implementation, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`),
		run.ID, run.ClipHash, run.AuthoritySHA256, run.SourceSHA256, run.SourceBytes,
		run.CertificationSHA256, run.PolicySHA256, run.Implementation, fillerDecisionEpoch(run.CreatedAt))
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
		&run.SourceBytes, &run.CertificationSHA256, &run.PolicySHA256, &run.Implementation,
		&createdAt); err != nil {
		return fillersafety.LedgerRun{}, err
	}
	run.CreatedAt = fromFillerDecisionEpoch(createdAt)
	return run, nil
}

func (s *sqlStore) AppendSpokenSafetyEvent(ctx context.Context, event fillersafety.LedgerEvent) error {
	payload, err := fillersafety.CanonicalLedgerEvent(event)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin spoken-safety event: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit spoken-safety event: %w", err)
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
		for _, earlier := range prior {
			if earlier.ID == event.Settle.ReservationEventID && earlier.Reserve != nil &&
				earlier.Reserve.EvaluationID == event.Settle.EvaluationID {
				return true
			}
		}
		return false
	}
	if event.Terminal != nil {
		ids := make([]string, 0, len(prior))
		for _, earlier := range prior {
			ids = append(ids, earlier.ID)
		}
		return slices.Equal(ids, event.Terminal.EventIDs)
	}
	return event.Kind == fillersafety.LedgerInferenceReserved
}
