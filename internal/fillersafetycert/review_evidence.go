package fillersafetycert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ModelReviewEvidenceSHA256 returns the canonical digest carried by a model
// review envelope and its final per-case attestations.
func ModelReviewEvidenceSHA256(evidence ModelReviewEvidence) (string, error) {
	raw, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validateModelReviewEvidence(review AuthorityReview) error {
	if review.Method != ReviewerModel {
		return nil
	}
	evidence := review.ModelEvidence
	if evidence == nil {
		return fmt.Errorf("model review lacks execution evidence")
	}
	digest, err := ModelReviewEvidenceSHA256(*evidence)
	if err != nil || digest != review.EvidenceSHA256 {
		return fmt.Errorf("model review evidence digest is invalid")
	}
	if evidence.SchemaVersion != ModelReviewEvidenceSchemaVersion ||
		evidence.ContractVersion != ModelReviewEvidenceContractVersion ||
		!validSHA256(evidence.PlanSHA256) || !validSHA256(evidence.WorklistSHA256) ||
		!validSHA256(evidence.PolicySHA256) || !validSHA256(evidence.SnapshotSHA256) ||
		!boundedID(evidence.RequestedModel) || !boundedID(evidence.ResolvedModel) ||
		!boundedEvidenceText(evidence.UpstreamProvider) || !boundedID(evidence.UpstreamProviderSlug) ||
		evidence.ModelFamily != review.ModelFamily || !validSHA256(evidence.PromptSHA256) ||
		!validSHA256(evidence.SchemaSHA256) || !boundedEvidenceText(evidence.FFmpeg.Version) ||
		!validSHA256(evidence.FFmpeg.BinarySHA256) || evidence.StartedAt.IsZero() ||
		evidence.CompletedAt.IsZero() || evidence.CompletedAt.Before(evidence.StartedAt) ||
		!evidence.CompletedAt.Equal(review.SubmittedAt) || evidence.MaximumRequests < len(review.Assessments) ||
		evidence.MaximumChargeNanoUSD <= 0 || evidence.MaximumSpendNanoUSD <= 0 ||
		evidence.MaximumChargeNanoUSD > evidence.MaximumSpendNanoUSD || evidence.Requests != len(evidence.Attempts) ||
		evidence.Requests < len(review.Assessments) || evidence.Requests > evidence.MaximumRequests {
		return fmt.Errorf("model review evidence identity, route, tool, time, or ceilings are invalid")
	}
	assessmentIndex := 0
	attemptNumber := 0
	var promptTokens, completionTokens, chargedNanoUSD int64
	for _, attempt := range evidence.Attempts {
		if assessmentIndex >= len(review.Assessments) || attempt.CaseID != review.Assessments[assessmentIndex].CaseID {
			return fmt.Errorf("model review evidence attempts do not follow assessment order")
		}
		attemptNumber++
		if attempt.Attempt != attemptNumber || attempt.RequestedAt.IsZero() ||
			attempt.RequestedAt.Before(evidence.StartedAt) || attempt.RequestedAt.After(evidence.CompletedAt) ||
			!validSHA256(attempt.RequestSHA256) || !validSHA256(attempt.ResponseSHA256) ||
			!boundedEvidenceText(attempt.GenerationID) || attempt.PromptTokens < 0 || attempt.CompletionTokens < 0 ||
			attempt.ChargedNanoUSD < 0 || attempt.ChargedNanoUSD > evidence.MaximumChargeNanoUSD {
			return fmt.Errorf("model review evidence contains an invalid attempt")
		}
		switch attempt.State {
		case ModelReviewAttemptFailed:
			if !attempt.ReviewedAt.IsZero() || attempt.ObservationSHA256 != "" && !validSHA256(attempt.ObservationSHA256) {
				return fmt.Errorf("failed model review attempt has invalid observation evidence")
			}
		case ModelReviewAttemptAccepted:
			if attempt.ReviewedAt.IsZero() || attempt.ReviewedAt.Before(attempt.RequestedAt) ||
				attempt.ReviewedAt.After(evidence.CompletedAt) || !validSHA256(attempt.ObservationSHA256) {
				return fmt.Errorf("accepted model review attempt lacks bound observation")
			}
			assessmentIndex++
			attemptNumber = 0
		default:
			return fmt.Errorf("model review evidence contains a non-terminal attempt")
		}
		if promptTokens > int64(^uint64(0)>>1)-attempt.PromptTokens ||
			completionTokens > int64(^uint64(0)>>1)-attempt.CompletionTokens ||
			chargedNanoUSD > evidence.MaximumSpendNanoUSD-attempt.ChargedNanoUSD {
			return fmt.Errorf("model review evidence aggregate exceeds its ceiling")
		}
		promptTokens += attempt.PromptTokens
		completionTokens += attempt.CompletionTokens
		chargedNanoUSD += attempt.ChargedNanoUSD
	}
	if assessmentIndex != len(review.Assessments) || promptTokens != evidence.PromptTokens ||
		completionTokens != evidence.CompletionTokens || chargedNanoUSD != evidence.ChargedNanoUSD {
		return fmt.Errorf("model review evidence is incomplete or has inconsistent aggregates")
	}
	return nil
}

func boundedEvidenceText(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 128 && utf8.ValidString(value) &&
		!strings.ContainsFunc(value, func(char rune) bool { return char < ' ' || char == 0x7f })
}
