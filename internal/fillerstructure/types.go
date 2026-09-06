// Package fillerstructure owns the provider-neutral complete-timeline
// agreement policy shared by certification and production.
package fillerstructure

const (
	AssessmentMediaMaximumBytes           int64 = 64 << 20
	AssessmentMediaMaximumTimelineDriftMS int64 = 1_000
)

type Status string

const (
	StatusConfirmed Status = "confirmed"
	StatusHeld      Status = "held"
)

type Unit string

const (
	UnitStandalone       Unit = "standalone"
	UnitCompilation      Unit = "compilation"
	UnitProgrammeExcerpt Unit = "programme_excerpt"
	UnitProgrammeSpots   Unit = "programme_with_spots"
	UnitUnusable         Unit = "unusable"
	UnitUnclear          Unit = "unclear"
)

type Role string

const (
	RoleCommercial        Role = "commercial"
	RolePromo             Role = "promo"
	RoleBumper            Role = "bumper"
	RolePSA               Role = "psa"
	RoleStationID         Role = "station_id"
	RoleTrailer           Role = "trailer"
	RoleInterstitial      Role = "interstitial"
	RoleProgrammeFragment Role = "programme_fragment"
	RoleNonFiller         Role = "non_filler"
	RoleAmbiguous         Role = "ambiguous"
	RoleUnusable          Role = "unusable"
)

type Disposition string

const (
	DispositionFillerCandidate Disposition = "filler_candidate"
	DispositionNonFiller       Disposition = "non_filler"
	DispositionUnresolved      Disposition = "unresolved"
)

const (
	ReasonAgreement          = "independent_model_family_agreement"
	ReasonInvalidCandidate   = "invalid_candidate"
	ReasonOperationalFailure = "operational_failure"
	ReasonUnitDisagreement   = "unit_disagreement"
	ReasonUnsupportedUnit    = "unsupported_unit"
	ReasonRoleDisagreement   = "role_disagreement"
	ReasonIntervalCount      = "interval_count_disagreement"
	ReasonIntervalRole       = "interval_role_disagreement"
	ReasonBoundary           = "boundary_disagreement"
	ReasonUnresolvedInterval = "unresolved_interval"
)

type Source struct {
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	DurationMS int64  `json:"durationMs"`
}

// AssessmentMedia identifies the exact transformed bytes submitted to every
// complete-timeline assessor. Source remains the original timeline authority.
type AssessmentMedia struct {
	SHA256        string `json:"sha256"`
	Bytes         int64  `json:"bytes"`
	DurationMS    int64  `json:"durationMs"`
	ProfileSHA256 string `json:"profileSha256"`
	LineageSHA256 string `json:"lineageSha256"`
}

type Assessor struct {
	ID               string `json:"id"`
	ModelFamily      string `json:"modelFamily"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ModelDigest      string `json:"modelDigest"`
	CapabilitySHA256 string `json:"capabilitySha256"`
	PromptVersion    string `json:"promptVersion"`
	EvidenceContract string `json:"evidenceContract"`
	AssessmentSHA256 string `json:"assessmentSha256"`
}

type Segment struct {
	StartMS int64 `json:"startMs"`
	EndMS   int64 `json:"endMs"`
	Role    Role  `json:"role"`
}

type Candidate struct {
	Source      Source    `json:"source"`
	InputSHA256 string    `json:"inputSha256"`
	Assessor    Assessor  `json:"assessor"`
	Failure     string    `json:"failure,omitempty"`
	Unit        Unit      `json:"unit,omitempty"`
	Role        Role      `json:"role,omitempty"`
	Segments    []Segment `json:"segments,omitempty"`
}

type Request struct {
	Source              Source
	Input               AssessmentInput
	BoundaryToleranceMS int64
	Candidates          []Candidate
}

type DecisionSegment struct {
	StartMS     int64       `json:"startMs"`
	EndMS       int64       `json:"endMs"`
	Disposition Disposition `json:"disposition"`
	Role        Role        `json:"role"`
}

type Decision struct {
	Source      Source            `json:"source"`
	Input       AssessmentInput   `json:"input"`
	Status      Status            `json:"status"`
	ReasonCodes []string          `json:"reasonCodes"`
	Unit        Unit              `json:"unit,omitempty"`
	Role        Role              `json:"role,omitempty"`
	Segments    []DecisionSegment `json:"segments,omitempty"`
	Candidates  []Candidate       `json:"candidates"`
}
