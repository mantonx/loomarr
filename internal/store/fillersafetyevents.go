package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

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
	if err := fillersafety.ValidateLedgerAppend(prior, event); err != nil {
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
