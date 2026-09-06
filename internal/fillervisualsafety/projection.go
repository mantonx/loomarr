package fillervisualsafety

import (
	"errors"
	"slices"
	"strings"
)

const (
	DerivativeSetSchemaVersion   = 1
	DerivativeSetContractVersion = "filler-visual-derivative-set-v1"
	ProjectionSchemaVersion      = 1
	ProjectionContractVersion    = "filler-visual-source-projection-v1"
	MaximumMappedDerivatives     = 10_000
)

// DerivativeBinding is one exact derived artifact associated with the source master.
type DerivativeBinding struct {
	ID                    string `json:"id"`
	SHA256                string `json:"sha256"`
	Bytes                 int64  `json:"bytes"`
	SourceAuthoritySHA256 string `json:"sourceAuthoritySha256"`
	SourceSHA256          string `json:"sourceSha256"`
}

// DerivativeSet is a complete point-in-time enumeration. Complete is not inferred
// from a partial query: the persistence adapter must make that guarantee explicitly.
type DerivativeSet struct {
	SchemaVersion         int                 `json:"schemaVersion"`
	ContractVersion       string              `json:"contractVersion"`
	SourceAuthoritySHA256 string              `json:"sourceAuthoritySha256"`
	SourceSHA256          string              `json:"sourceSha256"`
	Complete              bool                `json:"complete"`
	Derivatives           []DerivativeBinding `json:"derivatives"`
	SHA256                string              `json:"sha256"`
}

type ProjectedDisposition string

const (
	DispositionQuarantine ProjectedDisposition = "quarantine"
	DispositionHold       ProjectedDisposition = "hold"
	DispositionNoSignal   ProjectedDisposition = "no_prohibited_visual_observed"
)

type ProjectedDerivative struct {
	ID          string               `json:"id"`
	SHA256      string               `json:"sha256"`
	Disposition ProjectedDisposition `json:"disposition"`
}

// Projection is an all-or-nothing source result over the exact derivative set.
// It never grants production admission, even for a no-signal visual result.
type Projection struct {
	SchemaVersion              int                   `json:"schemaVersion"`
	ContractVersion            string                `json:"contractVersion"`
	SourceAuthoritySHA256      string                `json:"sourceAuthoritySha256"`
	SourceSHA256               string                `json:"sourceSha256"`
	ResultSHA256               string                `json:"resultSha256"`
	DerivativeSetSHA256        string                `json:"derivativeSetSha256"`
	SourceDisposition          ProjectedDisposition  `json:"sourceDisposition"`
	Derivatives                []ProjectedDerivative `json:"derivatives"`
	ProductionAdmissionAllowed bool                  `json:"productionAdmissionAllowed"`
	SHA256                     string                `json:"sha256"`
}

func SealDerivativeSet(set DerivativeSet) (DerivativeSet, error) {
	set.SchemaVersion = DerivativeSetSchemaVersion
	set.ContractVersion = DerivativeSetContractVersion
	set.Derivatives = slices.Clone(set.Derivatives)
	slices.SortFunc(set.Derivatives, compareDerivativeBindings)
	set.SHA256 = DerivativeSetSHA256(set)
	if err := ValidateDerivativeSet(set); err != nil {
		return DerivativeSet{}, err
	}
	return set, nil
}

func ValidateDerivativeSet(set DerivativeSet) error {
	if set.SchemaVersion != DerivativeSetSchemaVersion || set.ContractVersion != DerivativeSetContractVersion ||
		!validDigest(set.SourceAuthoritySHA256) || !validDigest(set.SourceSHA256) || !set.Complete ||
		set.Derivatives == nil || len(set.Derivatives) > MaximumMappedDerivatives ||
		!slices.IsSortedFunc(set.Derivatives, compareDerivativeBindings) || set.SHA256 == "" ||
		set.SHA256 != DerivativeSetSHA256(set) {
		return errors.New("visual-safety derivative set identity is invalid")
	}
	for index, derivative := range set.Derivatives {
		if !validIdentity(derivative.ID) || !validDigest(derivative.SHA256) || derivative.Bytes <= 0 ||
			derivative.Bytes > MaximumSourceBytes || derivative.SourceAuthoritySHA256 != set.SourceAuthoritySHA256 ||
			derivative.SourceSHA256 != set.SourceSHA256 || index > 0 && set.Derivatives[index-1].ID == derivative.ID {
			return errors.New("visual-safety derivative set contains an invalid or repeated binding")
		}
	}
	return nil
}

func DerivativeSetSHA256(set DerivativeSet) string {
	set.SHA256 = ""
	return digestJSON(set)
}

// Project applies one validated source result to every exact mapped derivative.
func Project(authority SourceAuthority, result Result, set DerivativeSet) (Projection, error) {
	if ValidateSourceAuthority(authority) != nil || ValidateResult(result) != nil || ValidateDerivativeSet(set) != nil ||
		result.SourceAuthoritySHA256 != authority.SHA256 || result.PolicySHA256 != authority.PolicySHA256 ||
		set.SourceAuthoritySHA256 != authority.SHA256 || set.SourceSHA256 != authority.SourceSHA256 {
		return Projection{}, errors.New("visual-safety source projection input is invalid")
	}
	disposition := projectedDisposition(result.Outcome)
	projection := Projection{
		SchemaVersion: ProjectionSchemaVersion, ContractVersion: ProjectionContractVersion,
		SourceAuthoritySHA256: authority.SHA256, SourceSHA256: authority.SourceSHA256,
		ResultSHA256: result.SHA256, DerivativeSetSHA256: set.SHA256,
		SourceDisposition: disposition, Derivatives: make([]ProjectedDerivative, len(set.Derivatives)),
	}
	for index, derivative := range set.Derivatives {
		projection.Derivatives[index] = ProjectedDerivative{ID: derivative.ID, SHA256: derivative.SHA256, Disposition: disposition}
	}
	projection.SHA256 = ProjectionSHA256(projection)
	if err := ValidateProjection(projection, authority, result, set); err != nil {
		return Projection{}, err
	}
	return projection, nil
}

func ValidateProjection(projection Projection, authority SourceAuthority, result Result, set DerivativeSet) error {
	if ValidateSourceAuthority(authority) != nil || ValidateResult(result) != nil || ValidateDerivativeSet(set) != nil ||
		projection.SchemaVersion != ProjectionSchemaVersion || projection.ContractVersion != ProjectionContractVersion ||
		projection.SourceAuthoritySHA256 != authority.SHA256 || projection.SourceSHA256 != authority.SourceSHA256 ||
		projection.ResultSHA256 != result.SHA256 || projection.DerivativeSetSHA256 != set.SHA256 ||
		projection.SourceDisposition != projectedDisposition(result.Outcome) || len(projection.Derivatives) != len(set.Derivatives) ||
		projection.ProductionAdmissionAllowed || projection.SHA256 == "" || projection.SHA256 != ProjectionSHA256(projection) {
		return errors.New("visual-safety source projection identity is invalid")
	}
	for index, derivative := range projection.Derivatives {
		want := set.Derivatives[index]
		if derivative.ID != want.ID || derivative.SHA256 != want.SHA256 || derivative.Disposition != projection.SourceDisposition {
			return errors.New("visual-safety source projection is incomplete or drifted")
		}
	}
	return nil
}

func ProjectionSHA256(projection Projection) string {
	projection.SHA256 = ""
	return digestJSON(projection)
}

func compareDerivativeBindings(left, right DerivativeBinding) int {
	if order := strings.Compare(left.ID, right.ID); order != 0 {
		return order
	}
	return strings.Compare(left.SHA256, right.SHA256)
}

func projectedDisposition(outcome Outcome) ProjectedDisposition {
	switch outcome {
	case OutcomeQuarantine:
		return DispositionQuarantine
	case OutcomeNoSignal:
		return DispositionNoSignal
	default:
		return DispositionHold
	}
}
