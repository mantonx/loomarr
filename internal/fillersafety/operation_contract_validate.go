package fillersafety

import "slices"

// ValidateHostedCallReservation validates the domain-owned command before a
// persistence adapter opens a transaction.
func ValidateHostedCallReservation(command HostedCallReservation) error {
	if !boundedLedgerID(command.EventID) || !boundedLedgerID(command.RunID) ||
		!boundedLedgerID(command.EvaluationID) || !boundedLedgerID(command.ClipHash) ||
		!validSHA256(command.RequestSHA256) || !boundedLedgerID(command.Role) || !boundedLedgerID(command.Rung) ||
		!boundedPublicIdentity(command.RequestedProvider) || !boundedPublicIdentity(command.RequestedModel) ||
		!boundedPublicIdentity(command.UpstreamProvider) || command.Ordinal < 0 || command.CreatedAt.IsZero() ||
		command.DerivativeBytes <= 0 || command.DerivativeDurationMS <= 0 || command.DerivativePixels < 0 ||
		command.RequestedNanoUSD <= 0 || command.Budget.PerClipNanoUSD < 0 ||
		command.Budget.PerDayNanoUSD < 0 || command.Budget.PerRunNanoUSD < 0 {
		return ErrEvaluationInvalid
	}
	versions := []string{
		command.Versions.EvidenceSHA256, command.Versions.ExtractorSHA256,
		command.Versions.PromptSHA256, command.Versions.SchemaSHA256,
		command.Versions.TaxonomySHA256, command.Versions.CertificationSHA256,
		command.Versions.PolicySHA256, command.Versions.CapabilitySHA256,
	}
	if slices.ContainsFunc(versions, func(value string) bool { return !validSHA256(value) }) {
		return ErrEvaluationInvalid
	}
	if slices.Equal(command.Modalities, []string{"audio"}) {
		if !boundedLedgerID(command.CandidateID) {
			return ErrEvaluationInvalid
		}
		return nil
	}
	if command.CandidateID == "" && slices.Equal(command.Modalities, []string{"audio", "video"}) {
		return nil
	}
	return ErrEvaluationInvalid
}

// ValidateHostedCallSettlement rejects free-form or incomplete settlement
// facts before they can reach either the V62 row or spoken-safety ledger.
func ValidateHostedCallSettlement(command HostedCallSettlement) error {
	if !boundedLedgerID(command.EventID) || !boundedLedgerID(command.RunID) ||
		!boundedLedgerID(command.ReservationEventID) || command.Ordinal < 0 || command.CreatedAt.IsZero() ||
		command.ChargedNanoUSD < 0 || command.PromptTokens < 0 || command.CompletionTokens < 0 || command.LatencyMS < 0 {
		return ErrEvaluationInvalid
	}
	if command.Failure == FailureNone {
		if !validSHA256(command.ResponseSHA256) || !boundedPublicIdentity(command.ResolvedProvider) ||
			!boundedPublicIdentity(command.ResolvedModel) || !boundedPublicIdentity(command.UpstreamProvider) ||
			!boundedLedgerID(command.GenerationID) || !validInferenceOutcome(command.Outcome) ||
			!command.ChargeKnown || !validUSD(command.ChargedAmountUSD) {
			return ErrEvaluationInvalid
		}
		return nil
	}
	if !validSettlementFailure(command.Failure) || command.Failure == FailureInterrupted || command.Failure == FailureBudget ||
		command.Outcome != "" || !optionalSHA256(command.ResponseSHA256) ||
		!optionalPublicIdentity(command.ResolvedProvider) || !optionalPublicIdentity(command.ResolvedModel) ||
		!optionalPublicIdentity(command.UpstreamProvider) || !optionalLedgerID(command.GenerationID) {
		return ErrEvaluationInvalid
	}
	if command.ChargeKnown {
		if !validUSD(command.ChargedAmountUSD) {
			return ErrEvaluationInvalid
		}
		return nil
	}
	if command.ChargedAmountUSD != "" || command.ChargedNanoUSD != 0 {
		return ErrEvaluationInvalid
	}
	return nil
}
