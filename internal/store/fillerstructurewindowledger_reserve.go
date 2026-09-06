package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func (s *sqlStore) ReserveStructureWindowCall(ctx context.Context, reservation fillerstructurewindow.CallReservation, budget InferenceBudget) (fillerstructurewindow.CallReservationState, error) {
	if err := fillerstructurewindow.ValidateCallReservation(reservation); err != nil {
		return "", err
	}
	evaluation := structureWindowCallEvaluation(reservation)
	if err := validateInferenceReservation(evaluation, budget); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin structure window call reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing string
	err = tx.QueryRowContext(ctx, s.ph(`SELECT request_sha256 FROM filler_structure_window_call_ledger WHERE request_sha256 = ?`), reservation.RequestSHA256).Scan(&existing)
	if err == nil {
		return "", fillerstructurewindow.ErrCallLedgerConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("check structure window call reservation: %w", err)
	}
	evaluation, err = s.reserveInferenceEvaluation(ctx, tx, evaluation, budget)
	if err != nil {
		return "", err
	}
	state := fillerstructurewindow.CallReservationAccepted
	ledgerState := fillerstructurewindow.CallLedgerOpen
	if evaluation.State == InferenceHeldBudget {
		state = fillerstructurewindow.CallReservationHeldBudget
		ledgerState = fillerstructurewindow.CallLedgerHeldBudget
	}
	raw, err := json.Marshal(reservation)
	if err != nil {
		return "", fmt.Errorf("encode structure window call reservation: %w", err)
	}
	result, err := tx.ExecContext(ctx, s.ph(`INSERT INTO filler_structure_window_call_ledger (
		request_sha256, evaluation_id, source_sha256, media_set_sha256, window_ordinal,
		assessor_id, state, reservation_json, record_sha256, record_json, requested_at, assessed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, 0) ON CONFLICT DO NOTHING`),
		reservation.RequestSHA256, evaluation.ID, reservation.MediaSet.Plan.Source.SHA256,
		reservation.MediaSet.SHA256, reservation.WindowOrdinal, reservation.Assessor.ID,
		string(ledgerState), string(raw), epoch(reservation.RequestedAt))
	if err != nil {
		return "", fmt.Errorf("insert structure window call reservation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted != 1 {
		return "", fillerstructurewindow.ErrCallLedgerConflict
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit structure window call reservation: %w", err)
	}
	return state, nil
}

func structureWindowCallEvaluation(reservation fillerstructurewindow.CallReservation) InferenceEvaluation {
	media := reservation.MediaSet.Windows[reservation.WindowOrdinal].Media
	return InferenceEvaluation{
		ID: "structure-window-" + reservation.RequestSHA256, ClipHash: reservation.MediaSet.Plan.Source.SHA256,
		Role: "complete_timeline_structure", Rung: "window_video",
		RequestedProvider: reservation.Assessor.Provider, RequestedModel: reservation.Assessor.Model,
		UpstreamProvider: reservation.UpstreamProvider, Modalities: []string{"text", "video"},
		DerivativeBytes: media.Bytes, DerivativeDurationMS: media.DurationMS,
		EstimatedNanoUSD: reservation.MaximumChargeNanoUSD, ReservedNanoUSD: reservation.RequestedNanoUSD,
		Versions: InferenceVersions{
			Evidence: reservation.MediaSet.SHA256, Extractor: media.ProfileSHA256,
			Prompt: reservation.PromptSHA256, Schema: reservation.SchemaSHA256,
			Taxonomy: reservation.Assessor.PromptVersion, AdmissionPolicy: "structure-window-shadow-v1",
			RolePolicy: fillerstructure.ReducerContractVersion, CapabilitySnapshot: reservation.Assessor.CapabilitySHA256,
		},
		CreatedAt: reservation.RequestedAt,
	}
}
