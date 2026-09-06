package store

import (
	"context"
	"errors"
	"slices"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

// ReserveSpokenSafetyCall adapts the domain-owned call contract to the generic
// V62 inference budget row and commits it with the ledger event atomically.
func (s *sqlStore) ReserveSpokenSafetyCall(
	ctx context.Context,
	command fillersafety.HostedCallReservation,
) (fillersafety.LedgerEvent, error) {
	if err := fillersafety.ValidateHostedCallReservation(command); err != nil {
		return fillersafety.LedgerEvent{}, err
	}
	evaluation := InferenceEvaluation{
		ID: command.EvaluationID, ClipHash: command.ClipHash, RunID: command.RunID,
		Role: command.Role, Rung: command.Rung,
		RequestedProvider: command.RequestedProvider, RequestedModel: command.RequestedModel,
		UpstreamProvider: command.UpstreamProvider, Modalities: slices.Clone(command.Modalities),
		DerivativeBytes: command.DerivativeBytes, DerivativeDurationMS: command.DerivativeDurationMS,
		DerivativePixels: command.DerivativePixels, ReservedNanoUSD: command.RequestedNanoUSD,
		Versions: InferenceVersions{
			Evidence: command.Versions.EvidenceSHA256, Extractor: command.Versions.ExtractorSHA256,
			Prompt: command.Versions.PromptSHA256, Schema: command.Versions.SchemaSHA256,
			Taxonomy:           command.Versions.TaxonomySHA256,
			AdmissionPolicy:    command.Versions.CertificationSHA256,
			RolePolicy:         command.Versions.PolicySHA256,
			CapabilitySnapshot: command.Versions.CapabilitySHA256,
		},
		CreatedAt: command.CreatedAt,
	}
	_, event, err := s.ReserveSpokenSafetyInference(ctx, SpokenSafetyInferenceReservation{
		EventID: command.EventID, RunID: command.RunID, CandidateID: command.CandidateID,
		RequestSHA256: command.RequestSHA256, Ordinal: command.Ordinal, CreatedAt: command.CreatedAt,
	}, evaluation, InferenceBudget{
		PerClipNanoUSD: command.Budget.PerClipNanoUSD,
		PerDayNanoUSD:  command.Budget.PerDayNanoUSD,
		PerRunNanoUSD:  command.Budget.PerRunNanoUSD,
	})
	return event, err
}

// SettleSpokenSafetyCall maps only bounded response authority and a closed
// failure class. A provider error string never enters either durable record.
func (s *sqlStore) SettleSpokenSafetyCall(
	ctx context.Context,
	command fillersafety.HostedCallSettlement,
) (fillersafety.LedgerEvent, error) {
	if err := fillersafety.ValidateHostedCallSettlement(command); err != nil {
		return fillersafety.LedgerEvent{}, err
	}
	state := InferenceCompleted
	failureReason := ""
	if command.Failure != fillersafety.FailureNone {
		state = InferenceFailed
		failureReason = string(command.Failure)
	}
	_, event, err := s.SettleSpokenSafetyInference(ctx, SpokenSafetyInferenceSettlement{
		EventID: command.EventID, RunID: command.RunID,
		ReservationEventID: command.ReservationEventID, ResponseSHA256: command.ResponseSHA256,
		Failure: command.Failure, ChargeKnown: command.ChargeKnown,
		Ordinal: command.Ordinal, CreatedAt: command.CreatedAt,
	}, InferenceSettlement{
		ResolvedProvider: command.ResolvedProvider, ResolvedModel: command.ResolvedModel,
		UpstreamProvider: command.UpstreamProvider,
		Tokens:           InferenceTokens{Prompt: command.PromptTokens, Completion: command.CompletionTokens},
		ChargedAmount:    command.ChargedAmountUSD, ChargedCurrency: settlementCurrency(command.ChargeKnown),
		ChargedNanoUSD: command.ChargedNanoUSD, EstimatedNanoUSD: command.ChargedNanoUSD,
		LatencyMS: command.LatencyMS, Attempts: 1, GenerationID: command.GenerationID,
		Outcome: command.Outcome, FailureReason: failureReason, State: state, UpdatedAt: command.CreatedAt,
	})
	if errors.Is(err, ErrInferenceBudgetExceeded) && event.Settle != nil {
		return event, nil
	}
	return event, err
}

func settlementCurrency(known bool) string {
	if known {
		return "USD"
	}
	return ""
}
