package fillersafety

import (
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

func ValidateLedgerRun(run LedgerRun) error {
	if !boundedLedgerID(run.ID) || !boundedLedgerID(run.ClipHash) ||
		!validSHA256(run.AuthoritySHA256) || !validSHA256(run.SourceSHA256) ||
		!validSHA256(run.CertificationSHA256) || !validSHA256(run.PolicySHA256) || !validSHA256(run.ProposerSHA256) ||
		!boundedLedgerID(run.Implementation) || run.SourceBytes <= 0 || run.DurationMS <= 0 || run.CreatedAt.IsZero() {
		return ErrLedgerInvalid
	}
	return nil
}

// ValidateLedgerAppend owns the closed event grammar shared by every store
// backend and completed-run replay. It accepts only the next event for one run.
func ValidateLedgerAppend(prior []LedgerEvent, event LedgerEvent) error {
	if _, err := CanonicalLedgerEvent(event); err != nil || event.Ordinal != len(prior) {
		return ErrLedgerConflict
	}
	for index, earlier := range prior {
		if earlier.RunID != event.RunID || earlier.Ordinal != index || earlier.CreatedAt.After(event.CreatedAt) {
			return ErrLedgerConflict
		}
	}
	if len(prior) == 0 {
		if event.Kind == LedgerSourcePlanned {
			return nil
		}
		return ErrLedgerConflict
	}
	if prior[len(prior)-1].Kind == LedgerTerminal {
		return ErrLedgerConflict
	}
	if len(prior) == 1 {
		if event.Kind == LedgerProposalCompleted {
			return nil
		}
		return ErrLedgerConflict
	}
	if event.Kind == LedgerSourcePlanned || event.Kind == LedgerProposalCompleted {
		return ErrLedgerConflict
	}
	if event.Settle != nil {
		reservationFound := false
		for _, earlier := range prior {
			if earlier.ID == event.Settle.ReservationEventID && earlier.Reserve != nil &&
				earlier.Reserve.EvaluationID == event.Settle.EvaluationID {
				reservationFound = true
			}
			if earlier.Settle != nil && earlier.Settle.ReservationEventID == event.Settle.ReservationEventID {
				return ErrLedgerConflict
			}
		}
		if reservationFound {
			return nil
		}
		return ErrLedgerConflict
	}
	if event.Terminal != nil {
		ids := make([]string, 0, len(prior))
		unsettled := map[string]struct{}{}
		for _, earlier := range prior {
			ids = append(ids, earlier.ID)
			if earlier.Reserve != nil && earlier.Reserve.State == ReservationAccepted {
				unsettled[earlier.ID] = struct{}{}
			}
			if earlier.Settle != nil {
				if _, exists := unsettled[earlier.Settle.ReservationEventID]; !exists {
					return ErrLedgerConflict
				}
				delete(unsettled, earlier.Settle.ReservationEventID)
			}
		}
		if len(unsettled) == 0 && slices.Equal(ids, event.Terminal.EventIDs) {
			return nil
		}
		return ErrLedgerConflict
	}
	if event.Kind == LedgerInferenceReserved {
		return nil
	}
	return ErrLedgerConflict
}

func validEventPayload(event LedgerEvent) bool {
	switch event.Kind {
	case LedgerSourcePlanned:
		return event.Source != nil && validCompleteSpan(event.Source.Audio) && event.Source.Audio == event.Source.Video
	case LedgerProposalCompleted:
		return event.Proposal != nil && validProposalLedger(*event.Proposal)
	case LedgerInferenceReserved:
		return event.Reserve != nil && validReservation(*event.Reserve)
	case LedgerInferenceSettled:
		return event.Settle != nil && validSettlement(*event.Settle)
	case LedgerTerminal:
		return event.Terminal != nil && validTerminal(*event.Terminal)
	default:
		return false
	}
}

func validCompleteSpan(span Span) bool { return span.StartMS == 0 && span.EndMS > 0 }

func validProposalLedger(proposal ProposalCompleted) bool {
	if !validSHA256(proposal.ProposerSHA256) ||
		(proposal.State != ProposalComplete && proposal.State != ProposalFailed) ||
		len(proposal.Candidates) > maxProposedCandidates {
		return false
	}
	if proposal.State == ProposalFailed && len(proposal.Candidates) != 0 {
		return false
	}
	for index, candidate := range proposal.Candidates {
		if !boundedLedgerID(candidate.ID) || candidate.StartMS < 0 || candidate.EndMS <= candidate.StartMS ||
			candidate.EndMS-candidate.StartMS > maxProposedIntervalMS {
			return false
		}
		if index > 0 && (proposal.Candidates[index-1].StartMS > candidate.StartMS ||
			(proposal.Candidates[index-1].StartMS == candidate.StartMS && proposal.Candidates[index-1].EndMS >= candidate.EndMS)) {
			return false
		}
	}
	return true
}

func validReservation(reservation InferenceReserved) bool {
	if !boundedLedgerID(reservation.EvaluationID) || !validSHA256(reservation.RequestSHA256) ||
		!boundedPublicIdentity(reservation.RequestedProvider) || !boundedPublicIdentity(reservation.RequestedModel) ||
		!boundedPublicIdentity(reservation.UpstreamProvider) || !validSHA256(reservation.CapabilitySHA256) ||
		!validSHA256(reservation.PromptSHA256) || !optionalSHA256(reservation.SchemaSHA256) ||
		!optionalLedgerID(reservation.Role) || !optionalLedgerID(reservation.Rung) ||
		(reservation.Role == "") != (reservation.Rung == "") || reservation.DerivativeBytes < 0 ||
		reservation.DerivativeDurationMS < 0 || reservation.DerivativePixels < 0 ||
		reservation.RequestedNanoUSD < 0 || reservation.ReservedNanoUSD < 0 ||
		(reservation.CandidateID != "" && !boundedLedgerID(reservation.CandidateID)) || len(reservation.Modalities) == 0 ||
		(reservation.State != ReservationAccepted && reservation.State != ReservationHeldBudget) {
		return false
	}
	if reservation.State == ReservationAccepted && reservation.ReservedNanoUSD != reservation.RequestedNanoUSD ||
		reservation.State == ReservationHeldBudget && (reservation.RequestedNanoUSD == 0 || reservation.ReservedNanoUSD != 0) {
		return false
	}
	modalities := slices.Clone(reservation.Modalities)
	slices.Sort(modalities)
	if !slices.Equal(modalities, reservation.Modalities) || slices.ContainsFunc(modalities, func(value string) bool {
		return value != "audio" && value != "video"
	}) {
		return false
	}
	if len(slices.Compact(modalities)) != len(modalities) {
		return false
	}
	if slices.Equal(modalities, []string{"audio"}) {
		return reservation.CandidateID != ""
	}
	return reservation.CandidateID == "" && slices.Equal(modalities, []string{"audio", "video"})
}

func validSettlement(settlement InferenceSettled) bool {
	if !boundedLedgerID(settlement.ReservationEventID) || !boundedLedgerID(settlement.EvaluationID) ||
		settlement.ChargedNanoUSD < 0 || settlement.AccountedNanoUSD < 0 || settlement.PromptTokens < 0 || settlement.CompletionTokens < 0 {
		return false
	}
	switch settlement.State {
	case SettlementCompleted:
		return settlement.Failure == FailureNone && validSHA256(settlement.ResponseSHA256) &&
			boundedPublicIdentity(settlement.ResolvedProvider) && boundedPublicIdentity(settlement.ResolvedModel) &&
			boundedPublicIdentity(settlement.UpstreamProvider) && boundedLedgerID(settlement.GenerationID) &&
			validInferenceOutcome(settlement.Outcome) && settlement.ChargeKnown && validUSD(settlement.ChargedAmountUSD) &&
			settlement.AccountedNanoUSD == settlement.ChargedNanoUSD
	case SettlementFailed:
		return settlement.Failure != FailureInterrupted && validSettlementFailure(settlement.Failure) &&
			settlement.Outcome == "" && optionalSHA256(settlement.ResponseSHA256) &&
			optionalPublicIdentity(settlement.ResolvedProvider) && optionalPublicIdentity(settlement.ResolvedModel) &&
			optionalPublicIdentity(settlement.UpstreamProvider) && optionalLedgerID(settlement.GenerationID) &&
			(settlement.ChargeKnown && validUSD(settlement.ChargedAmountUSD) && settlement.AccountedNanoUSD == settlement.ChargedNanoUSD ||
				!settlement.ChargeKnown && settlement.ChargedAmountUSD == "" && settlement.ChargedNanoUSD == 0 && settlement.AccountedNanoUSD == 0)
	case SettlementUnknown:
		return settlement.Failure == FailureInterrupted && settlement.ResponseSHA256 == "" &&
			settlement.ResolvedProvider == "" && settlement.ResolvedModel == "" && settlement.UpstreamProvider == "" &&
			settlement.GenerationID == "" && settlement.Outcome == "" && !settlement.ChargeKnown &&
			settlement.ChargedAmountUSD == "" && settlement.ChargedNanoUSD == 0
	default:
		return false
	}
}

func validInferenceOutcome(value string) bool {
	switch value {
	case string(AudioDetected), string(AudioDetectedUnprojectable), string(AudioAbsent), string(AudioUnclear),
		string(AudioFailed), string(AudioInvalidResponse), string(VideoProhibited),
		string(VideoProhibitedUnprojectable), string(VideoNoSignal), string(VideoIncomplete):
		return true
	default:
		return false
	}
}

func optionalSHA256(value string) bool { return value == "" || validSHA256(value) }

func optionalLedgerID(value string) bool { return value == "" || boundedLedgerID(value) }

func optionalPublicIdentity(value string) bool { return value == "" || boundedPublicIdentity(value) }

func validTerminal(terminal TerminalResult) bool {
	if _, valid := validateEvidence(terminal.Evidence); !valid ||
		!reflect.DeepEqual(terminal.Result, Reduce(terminal.Evidence)) || len(terminal.EventIDs) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(terminal.EventIDs))
	for _, id := range terminal.EventIDs {
		if !boundedLedgerID(id) {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	for _, candidate := range terminal.Evidence.Candidates {
		if !boundedLedgerID(candidate.ID) {
			return false
		}
	}
	for _, assessment := range terminal.Evidence.Audio {
		if !boundedLedgerID(assessment.CandidateID) {
			return false
		}
	}
	return true
}

func validSettlementFailure(value SettlementFailure) bool {
	switch value {
	case FailureTransport, FailureInvalidResponse, FailureRouteMismatch, FailureBudget, FailureInterrupted:
		return true
	default:
		return false
	}
}

func validUSD(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	dot := false
	for _, char := range value {
		if char == '.' && !dot {
			dot = true
			continue
		}
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != "."
}

func boundedLedgerID(value string) bool {
	if value == "" || len(value) > maxLedgerIDBytes || !utf8.ValidString(value) {
		return false
	}
	return strings.IndexFunc(value, func(char rune) bool {
		return (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '-'
	}) == -1
}

// boundedPublicIdentity bounds public provider/model labels without treating
// their namespaces as opaque ledger identifiers. Atomic helpers copy validated
// caller and V62 route identities into the ledger; full runtime binding remains
// staged by the design contract.
func boundedPublicIdentity(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxLedgerIDBytes || !utf8.ValidString(value) ||
		strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool { return char < ' ' || char == 0x7f })
}
