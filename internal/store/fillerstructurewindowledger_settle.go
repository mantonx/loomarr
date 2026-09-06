package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func (s *sqlStore) SettleStructureWindowCall(ctx context.Context, record fillerstructurewindow.CallRecord) error {
	if err := fillerstructurewindow.ValidateCallRecord(record); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin structure window call settlement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := s.getStructureWindowCallLedgerRow(ctx, tx, record.RequestSHA256, true)
	if err != nil {
		return err
	}
	if row.Entry.State == fillerstructurewindow.CallLedgerSettled {
		if row.Entry.Record != nil && reflect.DeepEqual(*row.Entry.Record, record) {
			return nil
		}
		return fillerstructurewindow.ErrCallLedgerConflict
	}
	if err := fillerstructurewindow.ValidateCallLedgerEntry(fillerstructurewindow.CallLedgerEntry{
		Reservation: row.Entry.Reservation, State: fillerstructurewindow.CallLedgerSettled, Record: &record,
	}); err != nil {
		return fillerstructurewindow.ErrCallLedgerConflict
	}
	if row.Entry.State == fillerstructurewindow.CallLedgerHeldBudget {
		if record.State != fillerstructure.AssessmentRecordHeldBudget {
			return fillerstructurewindow.ErrCallLedgerConflict
		}
		evaluation, evaluationErr := scanInferenceEvaluation(tx.QueryRowContext(ctx,
			s.ph(inferenceEvaluationSelect+` WHERE id = ?`), row.EvaluationID))
		if evaluationErr != nil || evaluation.State != InferenceHeldBudget || evaluation.ReservedNanoUSD != 0 {
			return fillerstructurewindow.ErrCallLedgerConflict
		}
	} else {
		if record.State == fillerstructure.AssessmentRecordHeldBudget {
			return fillerstructurewindow.ErrCallLedgerConflict
		}
		settled, overBudget, settleErr := s.settleInferenceEvaluation(ctx, tx, row.EvaluationID, structureWindowCallSettlement(record, row.Entry.Reservation))
		if settleErr != nil {
			return fmt.Errorf("settle structure window inference accounting: %w", settleErr)
		}
		if overBudget != (record.State == fillerstructure.AssessmentRecordOverReservation) ||
			settled.ChargedNanoUSD != record.ChargedNanoUSD || settled.ReservedNanoUSD != record.AccountedNanoUSD {
			return fillerstructurewindow.ErrCallLedgerConflict
		}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode structure window call settlement: %w", err)
	}
	result, err := tx.ExecContext(ctx, s.ph(`UPDATE filler_structure_window_call_ledger SET
		state = ?, record_sha256 = ?, record_json = ?, assessed_at = ?
		WHERE request_sha256 = ? AND state = ?`), string(fillerstructurewindow.CallLedgerSettled),
		record.SHA256, string(raw), epoch(record.AssessedAt), record.RequestSHA256, string(row.Entry.State))
	if err != nil {
		return fmt.Errorf("write structure window call settlement: %w", err)
	}
	if updated, updateErr := result.RowsAffected(); updateErr != nil || updated != 1 {
		return fillerstructurewindow.ErrCallLedgerConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit structure window call settlement: %w", err)
	}
	return nil
}

func structureWindowCallSettlement(record fillerstructurewindow.CallRecord, reservation fillerstructurewindow.CallReservation) InferenceSettlement {
	state, outcome := InferenceFailed, ""
	if record.State == fillerstructure.AssessmentRecordAccepted {
		state, outcome = InferenceCompleted, "window_complete"
	}
	currency := ""
	if record.ChargeKnown {
		currency = "USD"
	}
	return InferenceSettlement{
		ResolvedProvider: record.ResolvedProvider, ResolvedModel: record.ResolvedModel, UpstreamProvider: record.UpstreamProvider,
		Tokens: InferenceTokens{
			Prompt: record.Tokens.Prompt, Completion: record.Tokens.Completion, Reasoning: record.Tokens.Reasoning,
			Cached: record.Tokens.Cached, CacheWrite: record.Tokens.CacheWrite, Image: record.Tokens.Image,
			Audio: record.Tokens.Audio, Video: record.Tokens.Video,
		},
		ChargedAmount: record.ChargedAmountUSD, ChargedCurrency: currency, ChargedNanoUSD: record.ChargedNanoUSD,
		EstimatedNanoUSD: reservation.MaximumChargeNanoUSD, PriceSnapshot: record.MetadataSnapshotSHA256,
		Attempts: structureWindowCallAttempts(record), GenerationID: record.GenerationID, Outcome: outcome,
		FailureReason: record.Failure, State: state, RetainReservation: record.State == fillerstructure.AssessmentRecordUnsettled,
		UpdatedAt: record.AssessedAt,
	}
}

func structureWindowCallAttempts(record fillerstructurewindow.CallRecord) int {
	if record.ResponseSHA256 == "" {
		return 0
	}
	return 1
}
