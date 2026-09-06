package fillerstructure

import (
	"errors"
	"slices"
	"strings"
)

func ValidateAssessmentRecord(record AssessmentRecord) error {
	if record.SchemaVersion != AssessmentRecordSchemaVersion || record.ContractVersion != AssessmentRecordContractVersion ||
		!digest(record.SHA256) || record.SHA256 != AssessmentRecordSHA256(record) || !validSource(record.Source) ||
		!validAssessmentMedia(record.Media, record.Source) || !validProfile(record.Assessor) ||
		!digest(record.MetadataSnapshotSHA256) ||
		!digest(record.PromptSHA256) || !digest(record.SchemaSHA256) || !digest(record.RequestSHA256) ||
		!canonicalIdentity(record.UpstreamProvider) || !canonicalIdentity(record.UpstreamProviderSlug) ||
		record.RequestedNanoUSD <= 0 || record.ReservedNanoUSD < 0 || record.ChargedNanoUSD < 0 ||
		record.AccountedNanoUSD < 0 || !validAssessmentTokens(record.Tokens) || record.AssessedAt.IsZero() ||
		record.AssessedAt != record.AssessedAt.UTC() {
		return errors.New("filler structure assessment record: identity or accounting is invalid")
	}
	if record.ResponseSHA256 != "" && !digest(record.ResponseSHA256) ||
		record.StructuredOutputSHA256 != "" && !digest(record.StructuredOutputSHA256) {
		return errors.New("filler structure assessment record: response identity is invalid")
	}
	switch record.State {
	case AssessmentRecordAccepted:
		if record.Failure != "" || record.Result == nil || record.ResponseSHA256 == "" || record.StructuredOutputSHA256 == "" ||
			!closedAssessmentRoute(record) || !closedAssessmentCharge(record) || record.ReservedNanoUSD != record.RequestedNanoUSD ||
			record.ChargedNanoUSD > record.ReservedNanoUSD || record.AccountedNanoUSD != record.ChargedNanoUSD ||
			!validAssessmentResult(*record.Result, record.Source.DurationMS) {
			return errors.New("filler structure assessment record: accepted result is incomplete")
		}
	case AssessmentRecordFailed:
		if !slices.Contains([]string{AssessmentFailureProvider, AssessmentFailureInvalidResponse, AssessmentFailureRouteMismatch}, record.Failure) ||
			record.Result != nil || !validFailedAssessmentResponse(record) || !closedAssessmentCharge(record) ||
			record.ReservedNanoUSD != record.RequestedNanoUSD || record.ChargedNanoUSD > record.ReservedNanoUSD ||
			record.AccountedNanoUSD != record.ChargedNanoUSD {
			return errors.New("filler structure assessment record: failed result is incomplete")
		}
	case AssessmentRecordUnsettled:
		if !slices.Contains([]string{AssessmentFailureUnsettled, AssessmentFailureTransport}, record.Failure) || record.Result != nil || record.ChargeKnown ||
			record.ChargedAmountUSD != "" || record.ChargedNanoUSD != 0 || record.ReservedNanoUSD != record.RequestedNanoUSD ||
			record.AccountedNanoUSD != record.ReservedNanoUSD {
			return errors.New("filler structure assessment record: unsettled result is invalid")
		}
	case AssessmentRecordHeldBudget:
		if record.Failure != AssessmentFailureBudget || record.Result != nil || record.ResponseSHA256 != "" ||
			record.StructuredOutputSHA256 != "" || record.ResolvedProvider != "" || record.ResolvedModel != "" ||
			record.GenerationID != "" || record.ChargeKnown || record.ChargedAmountUSD != "" || record.ChargedNanoUSD != 0 ||
			record.ReservedNanoUSD != 0 || record.AccountedNanoUSD != 0 {
			return errors.New("filler structure assessment record: budget hold is invalid")
		}
	case AssessmentRecordOverReservation:
		if record.Failure != AssessmentFailureOverReservation || record.Result != nil || record.ResponseSHA256 == "" ||
			!closedAssessmentCharge(record) || record.ReservedNanoUSD != record.RequestedNanoUSD ||
			record.ChargedNanoUSD <= record.ReservedNanoUSD || record.AccountedNanoUSD != record.ChargedNanoUSD {
			return errors.New("filler structure assessment record: over-reservation result is invalid")
		}
	default:
		return errors.New("filler structure assessment record: state is invalid")
	}
	return nil
}

func validFailedAssessmentResponse(record AssessmentRecord) bool {
	if record.ResponseSHA256 == "" {
		return false
	}
	if record.Failure == AssessmentFailureInvalidResponse {
		return closedAssessmentRoute(record)
	}
	return record.ResolvedProvider == "" && record.ResolvedModel == ""
}

func validAssessmentResult(result AssessmentResult, durationMS int64) bool {
	candidate := Candidate{Unit: result.Unit, Role: result.Role, Segments: result.Segments}
	if !validUnit(result.Unit) || !validCandidateRole(candidate) || !completeTimeline(result.Segments, durationMS) {
		return false
	}
	segments := make([]DirectVideoAssessmentSegment, 0, len(result.Segments))
	for _, segment := range result.Segments {
		segments = append(segments, DirectVideoAssessmentSegment{
			StartMS: segment.StartMS, EndMS: segment.EndMS, Role: segment.Role,
		})
	}
	unit, role := deriveDirectVideoClaims(segments)
	if Unit(unit.Kind) != result.Unit {
		return false
	}
	return role == nil && result.Role == "" || role != nil && Role(role.Kind) == result.Role
}

func closedAssessmentRoute(record AssessmentRecord) bool {
	return canonicalIdentity(record.ResolvedProvider) && canonicalIdentity(record.ResolvedModel) && canonicalIdentity(record.GenerationID)
}

func closedAssessmentCharge(record AssessmentRecord) bool {
	return record.ChargeKnown && validAssessmentUSD(record.ChargedAmountUSD)
}

func validAssessmentTokens(tokens AssessmentTokenUsage) bool {
	return tokens.Prompt >= 0 && tokens.Completion >= 0 && tokens.Reasoning >= 0 && tokens.Cached >= 0 &&
		tokens.CacheWrite >= 0 && tokens.Image >= 0 && tokens.Audio >= 0 && tokens.Video >= 0
}

func validAssessmentUSD(value string) bool {
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
