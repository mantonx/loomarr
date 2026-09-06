package fillervisualsafety

import (
	"errors"
	"slices"
)

const (
	ResultSchemaVersion   = 1
	ResultContractVersion = "filler-visual-safety-result-v1"
)

type Outcome string

const (
	OutcomeQuarantine Outcome = "quarantine"
	OutcomeHold       Outcome = "hold"
	OutcomeNoSignal   Outcome = "no_prohibited_visual_observed"
)

type Reason string

const (
	ReasonPortableProhibited Reason = "portable_prohibited_signal"
	ReasonDirectProhibited   Reason = "direct_video_prohibited_signal"
	ReasonAppleProhibited    Reason = "apple_sca_prohibited_signal"
	ReasonPortableMissing    Reason = "portable_coverage_missing"
	ReasonCoverageIncomplete Reason = "portable_coverage_incomplete"
	ReasonLaneIncomplete     Reason = "visual_lane_incomplete"
	ReasonLaneFailed         Reason = "visual_lane_failed"
	ReasonInvalidEvidence    Reason = "invalid_visual_evidence"
)

// Result is safety evidence only. QuarantineRequired is deliberately one-way;
// ProductionAdmissionAllowed is always false.
type Result struct {
	SchemaVersion              int      `json:"schemaVersion"`
	ContractVersion            string   `json:"contractVersion"`
	SourceAuthoritySHA256      string   `json:"sourceAuthoritySha256"`
	PolicySHA256               string   `json:"policySha256"`
	CoverageEvidenceSHA256     string   `json:"coverageEvidenceSha256"`
	ObservationSHA256s         []string `json:"observationSha256s"`
	Outcome                    Outcome  `json:"outcome"`
	Reasons                    []Reason `json:"reasons"`
	QuarantineRequired         bool     `json:"quarantineRequired"`
	ProductionAdmissionAllowed bool     `json:"productionAdmissionAllowed"`
	SHA256                     string   `json:"sha256"`
}

// Reduce applies the visual-safety asymmetry: a valid positive outranks every
// negative and hold, while no collection of negatives grants admission.
func Reduce(authority SourceAuthority, coverage CoverageEvidence, plan CoveragePlan, observations []Observation) Result {
	result := Result{
		SchemaVersion: ResultSchemaVersion, ContractVersion: ResultContractVersion,
		SourceAuthoritySHA256: authority.SHA256, PolicySHA256: authority.PolicySHA256,
		CoverageEvidenceSHA256: coverage.SHA256, ObservationSHA256s: []string{}, Reasons: []Reason{},
	}
	authorityValid := ValidateSourceAuthority(authority) == nil
	coverageValid := authorityValid && plan.SourceAuthoritySHA256 == authority.SHA256 &&
		plan.SourceSHA256 == authority.SourceSHA256 && plan.DurationMS == authority.DurationMS &&
		plan.Video == authority.Video &&
		ValidateCoverageEvidence(plan, coverage) == nil

	seen := make(map[ProducerFamily]struct{}, 3)
	validPositive := false
	portableValid := false
	for _, observation := range observations {
		valid := ValidateObservation(observation) == nil && authorityValid &&
			observation.SourceAuthoritySHA256 == authority.SHA256 && observation.SourceSHA256 == authority.SourceSHA256 &&
			observation.PolicySHA256 == authority.PolicySHA256 && !observation.AssessedAt.Before(authority.MeasuredAt) &&
			observationFitsDuration(observation, authority.DurationMS)
		if _, duplicate := seen[observation.Profile.Family]; duplicate {
			valid = false
		} else {
			seen[observation.Profile.Family] = struct{}{}
		}
		if observation.Profile.Family == ProducerPortable {
			valid = valid && coverageValid && observation.CoverageEvidenceSHA256 == coverage.SHA256
			portableValid = valid
		}
		if !valid {
			result.Reasons = append(result.Reasons, ReasonInvalidEvidence)
			continue
		}
		result.ObservationSHA256s = append(result.ObservationSHA256s, observation.SHA256)
		switch observation.State {
		case ObservationProhibited:
			validPositive = true
			switch observation.Profile.Family {
			case ProducerPortable:
				result.Reasons = append(result.Reasons, ReasonPortableProhibited)
			case ProducerDirectVideo:
				result.Reasons = append(result.Reasons, ReasonDirectProhibited)
			case ProducerAppleSCA:
				result.Reasons = append(result.Reasons, ReasonAppleProhibited)
			}
		case ObservationIncomplete:
			result.Reasons = append(result.Reasons, ReasonLaneIncomplete)
		case ObservationFailed:
			result.Reasons = append(result.Reasons, ReasonLaneFailed)
		}
	}
	if !coverageValid {
		result.Reasons = append(result.Reasons, ReasonCoverageIncomplete)
	}
	if !portableValid {
		result.Reasons = append(result.Reasons, ReasonPortableMissing)
	}
	slices.Sort(result.ObservationSHA256s)
	slices.Sort(result.Reasons)
	result.Reasons = slices.Compact(result.Reasons)
	if validPositive {
		result.Outcome = OutcomeQuarantine
		result.QuarantineRequired = true
	} else if len(result.Reasons) != 0 {
		result.Outcome = OutcomeHold
	} else {
		result.Outcome = OutcomeNoSignal
	}
	result.SHA256 = ResultSHA256(result)
	return result
}

func observationFitsDuration(observation Observation, durationMS int64) bool {
	for _, interval := range observation.Intervals {
		if interval.EndMS > durationMS {
			return false
		}
	}
	return true
}

func ValidateResult(result Result) error {
	if result.SchemaVersion != ResultSchemaVersion || result.ContractVersion != ResultContractVersion ||
		!validDigest(result.SourceAuthoritySHA256) || !validDigest(result.PolicySHA256) ||
		!validDigest(result.CoverageEvidenceSHA256) || result.ObservationSHA256s == nil || result.Reasons == nil ||
		!slices.IsSorted(result.ObservationSHA256s) || !slices.IsSorted(result.Reasons) ||
		result.ProductionAdmissionAllowed || result.SHA256 == "" || result.SHA256 != ResultSHA256(result) {
		return errors.New("visual-safety result identity is invalid")
	}
	for index, digest := range result.ObservationSHA256s {
		if !validDigest(digest) || index > 0 && digest == result.ObservationSHA256s[index-1] {
			return errors.New("visual-safety result repeats or contains an invalid observation")
		}
	}
	for index, reason := range result.Reasons {
		if !validReason(reason) || index > 0 && reason == result.Reasons[index-1] {
			return errors.New("visual-safety result repeats or contains an invalid reason")
		}
	}
	switch result.Outcome {
	case OutcomeQuarantine:
		if !result.QuarantineRequired || !containsProhibitedReason(result.Reasons) {
			return errors.New("visual-safety quarantine lacks a prohibited signal")
		}
	case OutcomeHold:
		if result.QuarantineRequired || len(result.Reasons) == 0 || containsProhibitedReason(result.Reasons) {
			return errors.New("visual-safety hold is invalid")
		}
	case OutcomeNoSignal:
		if result.QuarantineRequired || len(result.Reasons) != 0 {
			return errors.New("visual-safety no-signal result is invalid")
		}
	default:
		return errors.New("visual-safety result outcome is invalid")
	}
	return nil
}

func ResultSHA256(result Result) string {
	result.SHA256 = ""
	return digestJSON(result)
}

func validReason(reason Reason) bool {
	switch reason {
	case ReasonPortableProhibited, ReasonDirectProhibited, ReasonAppleProhibited, ReasonPortableMissing,
		ReasonCoverageIncomplete, ReasonLaneIncomplete, ReasonLaneFailed, ReasonInvalidEvidence:
		return true
	default:
		return false
	}
}

func containsProhibitedReason(reasons []Reason) bool {
	return slices.Contains(reasons, ReasonPortableProhibited) || slices.Contains(reasons, ReasonDirectProhibited) ||
		slices.Contains(reasons, ReasonAppleProhibited)
}
