package fillersafetyreview

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func validateCheckpoint(value checkpoint) error {
	identity := value.Identity
	if identity.SchemaVersion != checkpointSchemaVersion || !validSHA256(identity.PlanSHA256) ||
		!validSHA256(identity.DraftSHA256) || !validSHA256(identity.WorklistSHA256) ||
		!validSHA256(identity.PolicySHA256) || !validSHA256(identity.SnapshotSHA256) ||
		!boundedID(identity.ReviewerID) || !boundedID(identity.ModelFamily) || !boundedID(identity.Model) ||
		!boundedID(identity.ResolvedModel) || !boundedText(identity.UpstreamProvider) ||
		!boundedID(identity.UpstreamProviderSlug) || !validSHA256(identity.PromptSHA256) ||
		!validSHA256(identity.SchemaSHA256) || !boundedText(identity.FFmpeg.Version) ||
		!validSHA256(identity.FFmpeg.BinarySHA256) || identity.ExpectedCases <= 0 ||
		identity.MaximumRequests < identity.ExpectedCases || identity.MaximumRequests > identity.ExpectedCases+16 ||
		identity.MaximumChargeNanoUSD <= 0 || identity.MaximumSpendNanoUSD <= 0 || value.StartedAt.IsZero() ||
		!value.CompletedAt.IsZero() && value.CompletedAt.Before(value.StartedAt) || len(value.Attempts) > identity.MaximumRequests ||
		len(value.Accepted) > identity.ExpectedCases {
		return fmt.Errorf("model review checkpoint identity, time, or counts are invalid")
	}
	acceptedByCase := make(map[string]acceptedCase, len(value.Accepted))
	for _, item := range value.Accepted {
		if item.Assessment.CaseID == "" || item.Attempt <= 0 {
			return fmt.Errorf("model review checkpoint contains an invalid accepted case")
		}
		if _, duplicate := acceptedByCase[item.Assessment.CaseID]; duplicate {
			return fmt.Errorf("model review checkpoint repeats an accepted case")
		}
		acceptedByCase[item.Assessment.CaseID] = item
	}
	seenAcceptedAttempts := map[string]struct{}{}
	var consumed int64
	for _, item := range value.Attempts {
		settled, settleErr := fillereval.USDToNanoCeil(item.ChargedAmountUSD)
		knownCharge := item.State == attemptAccepted || item.State == attemptFailed
		if !boundedID(item.CaseID) || item.Attempt <= 0 || item.RequestedAt.IsZero() ||
			item.RequestedAt.Before(value.StartedAt) || !validSHA256(item.RequestSHA256) ||
			item.ReservedNanoUSD != identity.MaximumChargeNanoUSD || item.PromptTokens < 0 ||
			item.CompletionTokens < 0 || item.ChargedNanoUSD < 0 ||
			item.ChargedNanoUSD > identity.MaximumChargeNanoUSD ||
			(knownCharge && (settleErr != nil || settled != item.ChargedNanoUSD)) ||
			(!knownCharge && (item.ChargedAmountUSD != "" || item.ChargedNanoUSD != 0)) {
			return fmt.Errorf("model review checkpoint contains an invalid attempt")
		}
		cost := item.ChargedNanoUSD
		if !knownCharge {
			cost = item.ReservedNanoUSD
		}
		if consumed > identity.MaximumSpendNanoUSD-cost {
			return fmt.Errorf("model review checkpoint exceeds its spend ceiling")
		}
		consumed += cost
		switch item.State {
		case attemptReserved:
			if item.ResponseSHA256 != "" || item.GenerationID != "" || item.Failure != "" ||
				!item.ReviewedAt.IsZero() || item.ObservationSHA256 != "" || item.PromptTokens != 0 ||
				item.CompletionTokens != 0 {
				return fmt.Errorf("reserved model review attempt contains terminal evidence")
			}
		case attemptUnsettled:
			if item.Failure != failureProvider || !item.ReviewedAt.IsZero() || item.ObservationSHA256 != "" ||
				(item.ResponseSHA256 != "" && !validSHA256(item.ResponseSHA256)) {
				return fmt.Errorf("unsettled model review attempt is invalid")
			}
		case attemptFailed:
			if item.Failure != failureProvider && item.Failure != failureInvalidResponse && item.Failure != failureUnclear ||
				!validSHA256(item.ResponseSHA256) || !boundedText(item.GenerationID) ||
				!item.ReviewedAt.IsZero() || item.ObservationSHA256 != "" && !validSHA256(item.ObservationSHA256) {
				return fmt.Errorf("failed model review attempt is invalid")
			}
		case attemptAccepted:
			accepted, ok := acceptedByCase[item.CaseID]
			key := fmt.Sprintf("%s\x00%d", item.CaseID, item.Attempt)
			if !ok || accepted.Attempt != item.Attempt || item.Failure != "" ||
				!validSHA256(item.ResponseSHA256) || !boundedText(item.GenerationID) || item.ReviewedAt.IsZero() ||
				item.ReviewedAt.Before(item.RequestedAt) || !validSHA256(item.ObservationSHA256) ||
				item.ObservationSHA256 != observationSHA256(accepted.Observation) {
				return fmt.Errorf("accepted model review attempt lacks exact observation binding")
			}
			if _, duplicate := seenAcceptedAttempts[key]; duplicate {
				return fmt.Errorf("model review checkpoint repeats an accepted attempt")
			}
			seenAcceptedAttempts[key] = struct{}{}
		default:
			return fmt.Errorf("model review checkpoint attempt state is invalid")
		}
	}
	if len(seenAcceptedAttempts) != len(value.Accepted) {
		return fmt.Errorf("model review checkpoint accepted case lacks an attempt")
	}
	if !value.CompletedAt.IsZero() && len(value.Accepted) != identity.ExpectedCases {
		return fmt.Errorf("incomplete model review checkpoint claims completion")
	}
	return nil
}

func validateCheckpointAgainstInputs(value checkpoint, loaded loadedInputs) error {
	attemptIndex, acceptedIndex := 0, 0
	for _, work := range loaded.worklist.Cases {
		expectedAttempt := 1
		accepted := false
		for attemptIndex < len(value.Attempts) && value.Attempts[attemptIndex].CaseID == work.CaseID {
			item := value.Attempts[attemptIndex]
			if item.Attempt != expectedAttempt || accepted {
				return fmt.Errorf("model review checkpoint attempts are not a canonical case prefix")
			}
			expectedAttempt++
			attemptIndex++
			if item.State != attemptAccepted {
				continue
			}
			if acceptedIndex >= len(value.Accepted) {
				return fmt.Errorf("model review checkpoint accepted attempt lacks a case")
			}
			acceptedCase := value.Accepted[acceptedIndex]
			expectedAssessment, err := assessmentFromObservation(work, loaded.policy, acceptedCase.Observation)
			if err != nil || acceptedCase.Attempt != item.Attempt ||
				!equalAssessment(acceptedCase.Assessment, expectedAssessment) {
				return fmt.Errorf("model review checkpoint accepted observation is invalid")
			}
			accepted = true
			acceptedIndex++
		}
		if !accepted {
			break
		}
	}
	if attemptIndex != len(value.Attempts) || acceptedIndex != len(value.Accepted) {
		return fmt.Errorf("model review checkpoint does not follow worklist order")
	}
	if !value.CompletedAt.IsZero() && acceptedIndex != len(loaded.worklist.Cases) {
		return fmt.Errorf("model review checkpoint completion is not exhaustive")
	}
	return nil
}

func checkpointSpend(value checkpoint) (int64, error) {
	var consumed int64
	for _, item := range value.Attempts {
		cost := item.ChargedNanoUSD
		if item.State == attemptReserved || item.State == attemptUnsettled {
			cost = item.ReservedNanoUSD
		}
		if consumed > value.Identity.MaximumSpendNanoUSD-cost {
			return 0, fmt.Errorf("model review spend ceiling is exhausted")
		}
		consumed += cost
	}
	return consumed, nil
}

func observationSHA256(value modelObservation) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return hashBytes(raw)
}

func equalAssessment(left, right fillersafetycert.ReviewAssessment) bool {
	return left.CaseID == right.CaseID && left.Decision == right.Decision &&
		slices.Equal(left.PositiveIntervals, right.PositiveIntervals)
}
