package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func (s *sqlStore) ReserveStructureAssessment(
	ctx context.Context,
	reservation fillerstructure.AssessmentReservation,
	budget InferenceBudget,
) (fillerstructure.AssessmentReservationState, error) {
	if err := fillerstructure.ValidateAssessmentReservation(reservation); err != nil {
		return "", err
	}
	evaluation := structureAssessmentEvaluation(reservation)
	if err := validateInferenceReservation(evaluation, budget); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin structure assessment reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing string
	err = tx.QueryRowContext(ctx, s.ph(`SELECT request_sha256 FROM filler_structure_assessment_ledger WHERE request_sha256 = ?`), reservation.RequestSHA256).Scan(&existing)
	if err == nil {
		return "", fillerstructure.ErrAssessmentLedgerConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("check structure assessment reservation: %w", err)
	}

	evaluation, err = s.reserveInferenceEvaluation(ctx, tx, evaluation, budget)
	if err != nil {
		return "", err
	}
	state := fillerstructure.AssessmentReservationAccepted
	ledgerState := fillerstructure.AssessmentLedgerOpen
	if evaluation.State == InferenceHeldBudget {
		state = fillerstructure.AssessmentReservationHeldBudget
		ledgerState = fillerstructure.AssessmentLedgerHeldBudget
	}
	raw, err := json.Marshal(reservation)
	if err != nil {
		return "", fmt.Errorf("encode structure assessment reservation: %w", err)
	}
	result, err := tx.ExecContext(ctx, s.ph(`INSERT INTO filler_structure_assessment_ledger (
		request_sha256, evaluation_id, source_sha256, assessor_id, state, reservation_json,
		assessment_sha256, record_json, requested_at, assessed_at
	) VALUES (?, ?, ?, ?, ?, ?, '', '', ?, 0) ON CONFLICT DO NOTHING`),
		reservation.RequestSHA256, evaluation.ID, reservation.Source.SHA256, reservation.Assessor.ID,
		string(ledgerState), string(raw), epoch(reservation.RequestedAt))
	if err != nil {
		return "", fmt.Errorf("insert structure assessment reservation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("inspect structure assessment reservation: %w", err)
	}
	if inserted != 1 {
		return "", fillerstructure.ErrAssessmentLedgerConflict
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit structure assessment reservation: %w", err)
	}
	return state, nil
}

func structureAssessmentEvaluation(reservation fillerstructure.AssessmentReservation) InferenceEvaluation {
	return InferenceEvaluation{
		ID: "structure-" + reservation.RequestSHA256, ClipHash: reservation.Source.SHA256,
		Role: "complete_timeline_structure", Rung: "complete_video",
		RequestedProvider: reservation.Assessor.Provider, RequestedModel: reservation.Assessor.Model,
		UpstreamProvider: reservation.UpstreamProvider, Modalities: []string{"text", "video"},
		DerivativeBytes: reservation.Media.Bytes, DerivativeDurationMS: reservation.Media.DurationMS,
		EstimatedNanoUSD: reservation.MaximumChargeNanoUSD, ReservedNanoUSD: reservation.RequestedNanoUSD,
		Versions: InferenceVersions{
			Evidence: reservation.Media.SHA256, Extractor: reservation.Media.ProfileSHA256,
			Prompt: reservation.PromptSHA256, Schema: reservation.SchemaSHA256,
			Taxonomy: reservation.Assessor.PromptVersion, AdmissionPolicy: "structure-shadow-v1",
			RolePolicy: fillerstructure.ReducerContractVersion, CapabilitySnapshot: reservation.Assessor.CapabilitySHA256,
		},
		CreatedAt: reservation.RequestedAt,
	}
}
