package fillersafety

import "slices"

// Reduce applies the asymmetric spoken-safety state machine. Valid presence
// outranks holds; holds outrank a candidate rejection. Invalid input is itself
// a hold and can never be reduced to a negative decision.
func Reduce(evidence Evidence) Result {
	reasons, valid := validateEvidence(evidence)
	if !valid {
		return result(OutcomeHold, reasons...)
	}
	if evidence.ProposalState == ProposalFailed {
		return result(OutcomeHold, ReasonProposalFailure)
	}

	var quarantine, holds []Reason
	for _, assessment := range evidence.Audio {
		switch assessment.State {
		case AudioDetected:
			quarantine = append(quarantine, ReasonAudioProhibitedSignal)
		case AudioDetectedUnprojectable:
			holds = append(holds, ReasonPresenceUnprojectable)
		case AudioAbsent:
		case AudioUnclear:
			holds = append(holds, ReasonAudioUnclear)
		case AudioFailed, AudioInvalidResponse:
			holds = append(holds, ReasonAudioFailure)
		}
	}
	if len(quarantine) > 0 {
		return result(OutcomeQuarantine, append(quarantine, holds...)...)
	}
	if len(holds) > 0 {
		return result(OutcomeHold, holds...)
	}

	switch evidence.Video {
	case VideoProhibited:
		return result(OutcomeQuarantine, ReasonVideoProhibitedSignal)
	case VideoProhibitedUnprojectable:
		return result(OutcomeHold, ReasonPresenceUnprojectable)
	case VideoNoSignal:
		return result(OutcomeCandidateRejected)
	case VideoIncomplete:
		return result(OutcomeHold, ReasonVideoIncomplete)
	case VideoFailed, VideoInvalidResponse:
		return result(OutcomeHold, ReasonVideoFailure)
	default:
		return result(OutcomeHold, ReasonInvalidEvidence)
	}
}

func result(outcome Outcome, reasons ...Reason) Result {
	slices.Sort(reasons)
	reasons = slices.Compact(reasons)
	if reasons == nil {
		reasons = []Reason{}
	}
	return Result{Outcome: outcome, Reasons: reasons}
}
