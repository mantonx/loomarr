package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const structureAssessmentLedgerSelect = `SELECT request_sha256, evaluation_id, source_sha256,
	assessor_id, state, reservation_json, assessment_sha256, record_json, requested_at, assessed_at
	FROM filler_structure_assessment_ledger`

type structureAssessmentLedgerRow struct {
	Entry        fillerstructure.AssessmentLedgerEntry
	EvaluationID string
}

func (s *sqlStore) GetStructureAssessmentLedgerEntry(ctx context.Context, requestSHA256 string) (fillerstructure.AssessmentLedgerEntry, error) {
	if requestSHA256 == "" {
		return fillerstructure.AssessmentLedgerEntry{}, fmt.Errorf("get structure assessment ledger entry: request identity is required")
	}
	row, err := s.getStructureAssessmentLedgerRow(ctx, s.db, requestSHA256, false)
	if err != nil {
		return fillerstructure.AssessmentLedgerEntry{}, err
	}
	return row.Entry, nil
}

func (s *sqlStore) ListOpenStructureAssessmentLedgerEntries(ctx context.Context, limit int) ([]fillerstructure.AssessmentLedgerEntry, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("list open structure assessment ledger entries: limit must be between 1 and 1000")
	}
	rows, err := s.db.QueryContext(ctx, s.ph(structureAssessmentLedgerSelect+`
		WHERE state <> ? ORDER BY requested_at ASC, request_sha256 ASC LIMIT ?`),
		string(fillerstructure.AssessmentLedgerSettled), limit)
	if err != nil {
		return nil, fmt.Errorf("list open structure assessment ledger entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	entries := make([]fillerstructure.AssessmentLedgerEntry, 0)
	for rows.Next() {
		row, scanErr := scanStructureAssessmentLedgerRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		entries = append(entries, row.Entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list open structure assessment ledger entries: %w", err)
	}
	return entries, nil
}

func (s *sqlStore) getStructureAssessmentLedgerRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, requestSHA256 string, lock bool) (structureAssessmentLedgerRow, error) {
	query := structureAssessmentLedgerSelect + ` WHERE request_sha256 = ?`
	if lock && s.dialect == DialectPostgres {
		query += ` FOR UPDATE`
	}
	row, err := scanStructureAssessmentLedgerRow(queryer.QueryRowContext(ctx, s.ph(query), requestSHA256))
	if errors.Is(err, sql.ErrNoRows) {
		return structureAssessmentLedgerRow{}, ErrNotFound
	}
	if err != nil {
		return structureAssessmentLedgerRow{}, fmt.Errorf("read structure assessment ledger entry: %w", err)
	}
	return row, nil
}

func scanStructureAssessmentLedgerRow(scanner scannable) (structureAssessmentLedgerRow, error) {
	var requestSHA, evaluationID, sourceSHA, assessorID, state, reservationRaw, assessmentSHA, recordRaw string
	var requestedAt, assessedAt int64
	if err := scanner.Scan(&requestSHA, &evaluationID, &sourceSHA, &assessorID, &state, &reservationRaw,
		&assessmentSHA, &recordRaw, &requestedAt, &assessedAt); err != nil {
		return structureAssessmentLedgerRow{}, err
	}
	var reservation fillerstructure.AssessmentReservation
	if err := decodeStructureLedgerJSON([]byte(reservationRaw), &reservation); err != nil {
		return structureAssessmentLedgerRow{}, fmt.Errorf("decode structure assessment reservation: %w", err)
	}
	entry := fillerstructure.AssessmentLedgerEntry{Reservation: reservation, State: fillerstructure.AssessmentLedgerState(state)}
	if recordRaw != "" {
		var record fillerstructure.AssessmentRecord
		if err := decodeStructureLedgerJSON([]byte(recordRaw), &record); err != nil {
			return structureAssessmentLedgerRow{}, fmt.Errorf("decode structure assessment settlement: %w", err)
		}
		entry.Record = &record
	}
	if reservation.RequestSHA256 != requestSHA || reservation.Source.SHA256 != sourceSHA ||
		reservation.Assessor.ID != assessorID || epoch(reservation.RequestedAt) != requestedAt ||
		(entry.Record == nil && (assessmentSHA != "" || assessedAt != 0)) ||
		(entry.Record != nil && (entry.Record.SHA256 != assessmentSHA || epoch(entry.Record.AssessedAt) != assessedAt)) {
		return structureAssessmentLedgerRow{}, fmt.Errorf("structure assessment ledger columns drift from their documents")
	}
	if err := fillerstructure.ValidateAssessmentLedgerEntry(entry); err != nil {
		return structureAssessmentLedgerRow{}, err
	}
	return structureAssessmentLedgerRow{Entry: entry, EvaluationID: evaluationID}, nil
}

func decodeStructureLedgerJSON(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
