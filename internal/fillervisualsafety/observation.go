package fillervisualsafety

import (
	"errors"
	"slices"
	"time"
)

const (
	ObservationSchemaVersion    = 1
	ObservationContractVersion  = "filler-visual-observation-v1"
	MaximumObservationIntervals = 64
	MaximumPolicyMatches        = 64
)

type ProducerFamily string

const (
	ProducerPortable    ProducerFamily = "portable"
	ProducerDirectVideo ProducerFamily = "direct_video"
	ProducerAppleSCA    ProducerFamily = "apple_sca"
)

type ObservationState string

const (
	ObservationProhibited ObservationState = "prohibited"
	ObservationNoSignal   ObservationState = "no_signal"
	ObservationIncomplete ObservationState = "incomplete"
	ObservationFailed     ObservationState = "failed"
)

type Interval struct {
	StartMS int64 `json:"startMs"`
	EndMS   int64 `json:"endMs"`
}

// ProducerProfile identifies a lane without exposing provider prose or policy values.
type ProducerProfile struct {
	Family           ProducerFamily `json:"family"`
	Implementation   string         `json:"implementation"`
	CapabilitySHA256 string         `json:"capabilitySha256"`
	EvidenceContract string         `json:"evidenceContract"`
}

// Observation is one immutable lane result. PolicyMatchIDs are opaque identifiers;
// restricted descriptions belong only in the private evidence addressed by EvidenceSHA256.
type Observation struct {
	SchemaVersion          int              `json:"schemaVersion"`
	ContractVersion        string           `json:"contractVersion"`
	SourceAuthoritySHA256  string           `json:"sourceAuthoritySha256"`
	SourceSHA256           string           `json:"sourceSha256"`
	PolicySHA256           string           `json:"policySha256"`
	CoverageEvidenceSHA256 string           `json:"coverageEvidenceSha256,omitempty"`
	Profile                ProducerProfile  `json:"profile"`
	State                  ObservationState `json:"state"`
	Intervals              []Interval       `json:"intervals"`
	PolicyMatchIDs         []string         `json:"policyMatchIds"`
	EvidenceSHA256         string           `json:"evidenceSha256"`
	AssessedAt             time.Time        `json:"assessedAt"`
	SHA256                 string           `json:"sha256"`
}

func SealObservation(observation Observation) (Observation, error) {
	observation.SchemaVersion = ObservationSchemaVersion
	observation.ContractVersion = ObservationContractVersion
	observation.AssessedAt = observation.AssessedAt.UTC()
	observation.Intervals = slices.Clone(observation.Intervals)
	observation.PolicyMatchIDs = slices.Clone(observation.PolicyMatchIDs)
	observation.SHA256 = ObservationSHA256(observation)
	if err := ValidateObservation(observation); err != nil {
		return Observation{}, err
	}
	return observation, nil
}

func ValidateObservation(observation Observation) error {
	if observation.SchemaVersion != ObservationSchemaVersion || observation.ContractVersion != ObservationContractVersion ||
		!validDigest(observation.SourceAuthoritySHA256) || !validDigest(observation.SourceSHA256) ||
		!validDigest(observation.PolicySHA256) || !validProducerProfile(observation.Profile) ||
		!validDigest(observation.EvidenceSHA256) || observation.AssessedAt.IsZero() || observation.AssessedAt.Location() != time.UTC ||
		len(observation.Intervals) > MaximumObservationIntervals || len(observation.PolicyMatchIDs) > MaximumPolicyMatches ||
		observation.SHA256 == "" || observation.SHA256 != ObservationSHA256(observation) {
		return errors.New("visual-safety observation identity is invalid")
	}
	if observation.Profile.Family == ProducerPortable {
		if !validDigest(observation.CoverageEvidenceSHA256) {
			return errors.New("portable visual-safety observation lacks coverage evidence")
		}
	} else if observation.CoverageEvidenceSHA256 != "" {
		return errors.New("visual-safety observation carries unrelated frame coverage")
	}
	if !validIntervals(observation.Intervals) || !validPolicyMatchIDs(observation.PolicyMatchIDs) {
		return errors.New("visual-safety observation evidence is invalid")
	}
	switch observation.State {
	case ObservationProhibited:
		if len(observation.PolicyMatchIDs) == 0 ||
			(observation.Profile.Family != ProducerAppleSCA && len(observation.Intervals) == 0) {
			return errors.New("visual-safety prohibited observation lacks a bounded signal")
		}
	case ObservationNoSignal, ObservationIncomplete, ObservationFailed:
		if len(observation.Intervals) != 0 || len(observation.PolicyMatchIDs) != 0 {
			return errors.New("visual-safety non-prohibited observation carries a prohibited signal")
		}
	default:
		return errors.New("visual-safety observation state is invalid")
	}
	return nil
}

func ObservationSHA256(observation Observation) string {
	observation.SHA256 = ""
	return digestJSON(observation)
}

func validProducerProfile(profile ProducerProfile) bool {
	if profile.Family != ProducerPortable && profile.Family != ProducerDirectVideo && profile.Family != ProducerAppleSCA {
		return false
	}
	return validIdentity(profile.Implementation) && validDigest(profile.CapabilitySHA256) && validIdentity(profile.EvidenceContract)
}

func validIntervals(intervals []Interval) bool {
	var priorEnd int64
	for index, interval := range intervals {
		if interval.StartMS < 0 || interval.EndMS <= interval.StartMS || index > 0 && interval.StartMS < priorEnd {
			return false
		}
		priorEnd = interval.EndMS
	}
	return true
}

func validPolicyMatchIDs(ids []string) bool {
	if !slices.IsSorted(ids) {
		return false
	}
	for index, id := range ids {
		if !validIdentity(id) || index > 0 && ids[index-1] == id {
			return false
		}
	}
	return true
}
