package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func (s *sqlStore) SettleStructureAssessment(ctx context.Context, record fillerstructure.AssessmentRecord) error {
	if err := fillerstructure.ValidateAssessmentRecord(record); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin structure assessment settlement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := s.getStructureAssessmentLedgerRow(ctx, tx, record.RequestSHA256, true)
	if err != nil {
		return err
	}
	if row.Entry.State == fillerstructure.AssessmentLedgerSettled {
		if row.Entry.Record != nil && reflect.DeepEqual(*row.Entry.Record, record) {
			return nil
		}
		return fillerstructure.ErrAssessmentLedgerConflict
	}
	if err := fillerstructure.ValidateAssessmentLedgerEntry(fillerstructure.AssessmentLedgerEntry{
		Reservation: row.Entry.Reservation, State: fillerstructure.AssessmentLedgerSettled, Record: &record,
	}); err != nil {
		return fillerstructure.ErrAssessmentLedgerConflict
	}
	if row.Entry.State == fillerstructure.AssessmentLedgerHeldBudget {
		if record.State != fillerstructure.AssessmentRecordHeldBudget {
			return fillerstructure.ErrAssessmentLedgerConflict
		}
		evaluation, evaluationErr := scanInferenceEvaluation(tx.QueryRowContext(ctx,
			s.ph(inferenceEvaluationSelect+` WHERE id = ?`), row.EvaluationID))
		if evaluationErr != nil || evaluation.State != InferenceHeldBudget || evaluation.ReservedNanoUSD != 0 {
			return fillerstructure.ErrAssessmentLedgerConflict
		}
	} else {
		if record.State == fillerstructure.AssessmentRecordHeldBudget {
			return fillerstructure.ErrAssessmentLedgerConflict
		}
		settled, overBudget, settleErr := s.settleInferenceEvaluation(ctx, tx, row.EvaluationID, structureInferenceSettlement(record, row.Entry.Reservation))
		if settleErr != nil {
			return fmt.Errorf("settle structure inference accounting: %w", settleErr)
		}
		if overBudget != (record.State == fillerstructure.AssessmentRecordOverReservation) ||
			settled.ChargedNanoUSD != record.ChargedNanoUSD || settled.ReservedNanoUSD != record.AccountedNanoUSD {
			return fillerstructure.ErrAssessmentLedgerConflict
		}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode structure assessment settlement: %w", err)
	}
	result, err := tx.ExecContext(ctx, s.ph(`UPDATE filler_structure_assessment_ledger SET
		state = ?, assessment_sha256 = ?, record_json = ?, assessed_at = ?
		WHERE request_sha256 = ? AND state = ?`),
		string(fillerstructure.AssessmentLedgerSettled), record.SHA256, string(raw), epoch(record.AssessedAt),
		record.RequestSHA256, string(row.Entry.State))
	if err != nil {
		return fmt.Errorf("write structure assessment settlement: %w", err)
	}
	if updated, updateErr := result.RowsAffected(); updateErr != nil || updated != 1 {
		return fillerstructure.ErrAssessmentLedgerConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit structure assessment settlement: %w", err)
	}
	return nil
}

func structureInferenceSettlement(record fillerstructure.AssessmentRecord, reservation fillerstructure.AssessmentReservation) InferenceSettlement {
	state := InferenceFailed
	outcome := ""
	if record.State == fillerstructure.AssessmentRecordAccepted {
		state = InferenceCompleted
		outcome = string(record.Result.Unit)
	}
	currency := ""
	if record.ChargeKnown {
		currency = "USD"
	}
	return InferenceSettlement{
		ResolvedProvider: record.ResolvedProvider, ResolvedModel: record.ResolvedModel,
		UpstreamProvider: record.UpstreamProvider,
		Tokens: InferenceTokens{
			Prompt: record.Tokens.Prompt, Completion: record.Tokens.Completion, Reasoning: record.Tokens.Reasoning,
			Cached: record.Tokens.Cached, CacheWrite: record.Tokens.CacheWrite, Image: record.Tokens.Image,
			Audio: record.Tokens.Audio, Video: record.Tokens.Video,
		},
		ChargedAmount: record.ChargedAmountUSD, ChargedCurrency: currency, ChargedNanoUSD: record.ChargedNanoUSD,
		EstimatedNanoUSD: reservation.MaximumChargeNanoUSD, PriceSnapshot: record.MetadataSnapshotSHA256,
		Attempts: structureAssessmentAttempts(record), GenerationID: record.GenerationID, Outcome: outcome, FailureReason: record.Failure,
		State: state, RetainReservation: record.State == fillerstructure.AssessmentRecordUnsettled,
		UpdatedAt: record.AssessedAt,
	}
}

func structureAssessmentAttempts(record fillerstructure.AssessmentRecord) int {
	if record.ResponseSHA256 == "" {
		return 0
	}
	return 1
}
