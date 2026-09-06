package fillerstructure

import "time"

const (
	AssessmentRecordSchemaVersion   = 4
	AssessmentRecordContractVersion = "filler-structure-assessment-record-v4"
	AssessmentMaximumResponseBytes  = 256 << 10
)

type AssessmentRecordState string

const (
	AssessmentRecordAccepted        AssessmentRecordState = "accepted"
	AssessmentRecordFailed          AssessmentRecordState = "failed"
	AssessmentRecordUnsettled       AssessmentRecordState = "unsettled"
	AssessmentRecordHeldBudget      AssessmentRecordState = "held_budget"
	AssessmentRecordOverReservation AssessmentRecordState = "over_reservation"
)

const (
	AssessmentFailureTransport       = "transport"
	AssessmentFailureProvider        = "provider"
	AssessmentFailureInvalidResponse = "invalid_response"
	AssessmentFailureRouteMismatch   = "route_mismatch"
	AssessmentFailureBudget          = "budget"
	AssessmentFailureUnsettled       = "unsettled"
	AssessmentFailureOverReservation = "over_reservation"
)

type AssessmentTokenUsage struct {
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
	Reasoning  int64 `json:"reasoning"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cacheWrite"`
	Image      int64 `json:"image"`
	Audio      int64 `json:"audio"`
	Video      int64 `json:"video"`
}

type AssessmentResult struct {
	Unit     Unit      `json:"unit"`
	Role     Role      `json:"role,omitempty"`
	Segments []Segment `json:"segments"`
}

// AssessmentRecord is the path-free, content-addressed authority for one complete-timeline call.
// Raw provider and structured-output bytes live in the evidence repository and are joined by hash.
type AssessmentRecord struct {
	SchemaVersion          int                   `json:"schemaVersion"`
	ContractVersion        string                `json:"contractVersion"`
	Source                 Source                `json:"source"`
	Media                  AssessmentMedia       `json:"media"`
	Assessor               AssessorProfile       `json:"assessor"`
	MetadataSnapshotSHA256 string                `json:"metadataSnapshotSha256"`
	PromptSHA256           string                `json:"promptSha256"`
	SchemaSHA256           string                `json:"schemaSha256"`
	RequestSHA256          string                `json:"requestSha256"`
	ResponseSHA256         string                `json:"responseSha256,omitempty"`
	StructuredOutputSHA256 string                `json:"structuredOutputSha256,omitempty"`
	ResolvedProvider       string                `json:"resolvedProvider,omitempty"`
	ResolvedModel          string                `json:"resolvedModel,omitempty"`
	UpstreamProvider       string                `json:"upstreamProvider"`
	UpstreamProviderSlug   string                `json:"upstreamProviderSlug"`
	GenerationID           string                `json:"generationId,omitempty"`
	Tokens                 AssessmentTokenUsage  `json:"tokens"`
	RequestedNanoUSD       int64                 `json:"requestedNanoUsd"`
	ReservedNanoUSD        int64                 `json:"reservedNanoUsd"`
	ChargedAmountUSD       string                `json:"chargedAmountUsd,omitempty"`
	ChargedNanoUSD         int64                 `json:"chargedNanoUsd"`
	AccountedNanoUSD       int64                 `json:"accountedNanoUsd"`
	ChargeKnown            bool                  `json:"chargeKnown"`
	State                  AssessmentRecordState `json:"state"`
	Failure                string                `json:"failure,omitempty"`
	Result                 *AssessmentResult     `json:"result,omitempty"`
	AssessedAt             time.Time             `json:"assessedAt"`
	SHA256                 string                `json:"sha256"`
}

// RecordedAssessment carries repository bytes only while crossing the persistence seam.
type RecordedAssessment struct {
	Record           AssessmentRecord
	RawResponse      []byte
	StructuredOutput string
}

type AssessmentRecordInput struct {
	Source                 Source
	Media                  AssessmentMedia
	Assessor               AssessorProfile
	MetadataSnapshotSHA256 string
	PromptSHA256           string
	SchemaSHA256           string
	RequestSHA256          string
	RawResponse            []byte
	StructuredOutput       string
	ResolvedProvider       string
	ResolvedModel          string
	UpstreamProvider       string
	UpstreamProviderSlug   string
	GenerationID           string
	Tokens                 AssessmentTokenUsage
	RequestedNanoUSD       int64
	ReservedNanoUSD        int64
	ChargedAmountUSD       string
	ChargedNanoUSD         int64
	AccountedNanoUSD       int64
	ChargeKnown            bool
	State                  AssessmentRecordState
	Failure                string
	AssessedAt             time.Time
}
