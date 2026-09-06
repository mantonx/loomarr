package fillerairworthiness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

func normalizeAxisProfile(profile AxisProfile) (AxisProfile, error) {
	if !validAxis(profile.Axis) || !validToken(profile.EvidenceContract) ||
		!validSHA256(profile.PolicySHA256) || !validSHA256(profile.CertificationSHA256) ||
		!validSHA256(profile.ImplementationSHA256) {
		return AxisProfile{}, fmt.Errorf("airworthiness axis profile identity is invalid")
	}
	normalized := profile
	normalized.CertifiedFlags = slices.Clone(profile.CertifiedFlags)
	slices.Sort(normalized.CertifiedFlags)
	for index, flag := range normalized.CertifiedFlags {
		if !validFlag(flag) || !axisOwnsFlag(profile.Axis, flag) ||
			(index > 0 && normalized.CertifiedFlags[index-1] == flag) {
			return AxisProfile{}, fmt.Errorf("airworthiness axis profile has invalid certified flags")
		}
	}
	if normalized.CertifiedFlags == nil {
		normalized.CertifiedFlags = []Flag{}
	}
	return normalized, nil
}

// NormalizeAxisProfile validates and canonicalizes one immutable safety-axis
// authority for use at package boundaries.
func NormalizeAxisProfile(profile AxisProfile) (AxisProfile, error) {
	return normalizeAxisProfile(profile)
}

// ValidateAxisEvidence validates the closed public portion of one safety-axis
// observation record. Cross-axis profile and observation-id checks belong to
// Evaluator.Evaluate, where the complete document is available.
func ValidateAxisEvidence(evidence AxisEvidence, durationMS int64) error {
	profile, err := normalizeAxisProfile(evidence.Profile)
	if err != nil || evidence.Profile.Axis != profile.Axis || !validSHA256(evidence.SubjectSHA256) ||
		!validSHA256(evidence.EvidenceSHA256) || !validCoverage(evidence.Coverage) ||
		durationMS <= 0 || durationMS > maximumRenderedDurationMS || len(evidence.Observations) > maximumObservations ||
		(evidence.Coverage == CoverageFailed || evidence.Coverage == CoverageConflict) && len(evidence.Observations) > 0 {
		return fmt.Errorf("airworthiness axis evidence is invalid")
	}
	seen := make(map[string]struct{}, len(evidence.Observations))
	for _, observation := range evidence.Observations {
		if _, duplicate := seen[observation.ID]; duplicate ||
			!validObservation(observation, evidence.Profile.Axis, durationMS) ||
			!slices.Contains(profile.CertifiedFlags, observation.Flag) {
			return fmt.Errorf("airworthiness axis observation is invalid")
		}
		seen[observation.ID] = struct{}{}
	}
	return nil
}

func sameAxisProfile(actual, expected AxisProfile) bool {
	normalized, err := normalizeAxisProfile(actual)
	return err == nil && reflect.DeepEqual(normalized, expected)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			index > 0 && (character == '-' || character == '_' || character == '.' || character == ':') {
			continue
		}
		return false
	}
	return true
}

func validCoverage(value Coverage) bool {
	return value == CoverageComplete || value == CoverageIncomplete ||
		value == CoverageFailed || value == CoverageConflict
}

func validObservation(observation Observation, axis Axis, durationMS int64) bool {
	return validToken(observation.ID) && validFlag(observation.Flag) && axisOwnsFlag(axis, observation.Flag) &&
		validSeverity(observation.Severity) && validContext(observation.Context) &&
		observation.StartMS >= 0 && observation.EndMS > observation.StartMS && observation.EndMS <= durationMS
}

func finalizeDecision(decision Decision) Decision {
	decision.ReasonCodes = sortedCompact(decision.ReasonCodes)
	decision.ObservedFlags = sortedCompact(decision.ObservedFlags)
	decision.EvidenceSHA256s = sortedCompact(decision.EvidenceSHA256s)
	decision.HeldAxes = orderAxes(decision.HeldAxes)
	slices.SortFunc(decision.Triggers, compareTriggers)
	if decision.ReasonCodes == nil {
		decision.ReasonCodes = []Reason{}
	}
	if decision.ObservedFlags == nil {
		decision.ObservedFlags = []Flag{}
	}
	if decision.Triggers == nil {
		decision.Triggers = []Trigger{}
	}
	if decision.HeldAxes == nil {
		decision.HeldAxes = []Axis{}
	}
	if decision.EvidenceSHA256s == nil {
		decision.EvidenceSHA256s = []string{}
	}
	decision.SHA256 = decisionSHA256(decision)
	return decision
}

func sortedCompact[T ~string](values []T) []T {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

func orderAxes(values []Axis) []Axis {
	set := make(map[Axis]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]Axis, 0, len(set))
	for _, axis := range axisOrder {
		if _, exists := set[axis]; exists {
			result = append(result, axis)
		}
	}
	return result
}

func compareTriggers(left, right Trigger) int {
	leftKey := fmt.Sprintf("%s\x00%s\x00%020d\x00%020d\x00%s\x00%s\x00%s\x00%s\x00%s",
		left.Axis, left.ObservationID, left.StartMS, left.EndMS, left.Flag, left.Severity,
		left.Context, left.Effect, left.EvidenceSHA256)
	rightKey := fmt.Sprintf("%s\x00%s\x00%020d\x00%020d\x00%s\x00%s\x00%s\x00%s\x00%s",
		right.Axis, right.ObservationID, right.StartMS, right.EndMS, right.Flag, right.Severity,
		right.Context, right.Effect, right.EvidenceSHA256)
	return strings.Compare(leftKey, rightKey)
}

func decisionSHA256(decision Decision) string {
	decision.SHA256 = ""
	raw, err := json.Marshal(decision)
	if err != nil {
		panic(fmt.Sprintf("marshal airworthiness decision: %v", err))
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// ValidateDecision verifies that a decision is canonical, self-addressed, and
// contains only values from the public Airworthiness contract.
func ValidateDecision(decision Decision) error {
	if decision.SchemaVersion != DecisionSchemaVersion || decision.ContractVersion != DecisionContractVersion ||
		decision.PolicyVersion != PolicyVersion || decision.VocabularyVersion != VocabularyVersion ||
		!validProfile(decision.Profile) || !validSHA256(decision.SubjectSHA256) ||
		!validSHA256(decision.AuthoritySHA256) || !validVerdict(decision.Verdict) ||
		!validSHA256(decision.SHA256) {
		return fmt.Errorf("airworthiness decision identity is invalid")
	}
	for _, reason := range decision.ReasonCodes {
		if !validReason(reason) {
			return fmt.Errorf("airworthiness decision reason is invalid")
		}
	}
	for _, flag := range decision.ObservedFlags {
		if !validFlag(flag) {
			return fmt.Errorf("airworthiness decision flag is invalid")
		}
	}
	for _, axis := range decision.HeldAxes {
		if !validAxis(axis) {
			return fmt.Errorf("airworthiness decision held axis is invalid")
		}
	}
	for _, digest := range decision.EvidenceSHA256s {
		if !validSHA256(digest) {
			return fmt.Errorf("airworthiness decision evidence digest is invalid")
		}
	}
	for _, trigger := range decision.Triggers {
		if !validTrigger(trigger) {
			return fmt.Errorf("airworthiness decision trigger is invalid")
		}
	}
	if !validDecisionMeaning(decision) {
		return fmt.Errorf("airworthiness decision meaning is invalid")
	}
	canonical := finalizeDecision(decision)
	if !reflect.DeepEqual(decision, canonical) {
		return fmt.Errorf("airworthiness decision is not canonical or has an invalid digest")
	}
	return nil
}

func validDecisionMeaning(decision Decision) bool {
	hasReason := func(want Reason) bool { return slices.Contains(decision.ReasonCodes, want) }
	hasRejectTrigger := false
	hasReviewTrigger := false
	for _, trigger := range decision.Triggers {
		hasRejectTrigger = hasRejectTrigger || trigger.Effect == EffectReject
		hasReviewTrigger = hasReviewTrigger || trigger.Effect == EffectReview
	}
	switch decision.Verdict {
	case VerdictPass:
		return reflect.DeepEqual(decision.ReasonCodes, []Reason{ReasonEvidenceSatisfied}) &&
			len(decision.Triggers) == 0 && len(decision.HeldAxes) == 0
	case VerdictReject:
		return reflect.DeepEqual(decision.ReasonCodes, []Reason{ReasonProhibitedObservation}) &&
			hasRejectTrigger && !hasReviewTrigger && len(decision.HeldAxes) == 0
	case VerdictHold:
		return len(decision.ReasonCodes) > 0 && !hasReason(ReasonEvidenceSatisfied) &&
			!hasReason(ReasonProhibitedObservation) && !hasRejectTrigger &&
			hasReviewTrigger == hasReason(ReasonObservationRequiresReview)
	default:
		return false
	}
}

func validVerdict(verdict Verdict) bool {
	return verdict == VerdictPass || verdict == VerdictReject || verdict == VerdictHold
}

func validReason(reason Reason) bool {
	switch reason {
	case ReasonEvidenceSatisfied, ReasonProhibitedObservation, ReasonObservationRequiresReview,
		ReasonCoverageIncomplete, ReasonCertificationIncomplete, ReasonEvidenceInvalid,
		ReasonRestrictedArchive:
		return true
	default:
		return false
	}
}

func validTrigger(trigger Trigger) bool {
	return validToken(trigger.ObservationID) && validAxis(trigger.Axis) && validFlag(trigger.Flag) &&
		axisOwnsFlag(trigger.Axis, trigger.Flag) && validSeverity(trigger.Severity) &&
		validContext(trigger.Context) && trigger.StartMS >= 0 && trigger.EndMS > trigger.StartMS &&
		validSHA256(trigger.EvidenceSHA256) && (trigger.Effect == EffectReject || trigger.Effect == EffectReview)
}
