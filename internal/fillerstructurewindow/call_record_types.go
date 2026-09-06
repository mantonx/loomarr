package fillerstructurewindow

import (
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	CallRecordSchemaVersion   = 2
	CallRecordContractVersion = "filler-structure-window-call-record-v2"
)

// CallRecord is the path-free settlement authority for one provider call over one planned
// window. Provider bytes and the semantic assessment are joined through their content digests.
type CallRecord struct {
	SchemaVersion          int                                   `json:"schemaVersion"`
	ContractVersion        string                                `json:"contractVersion"`
	MediaSet               MediaSet                              `json:"mediaSet"`
	WindowOrdinal          int                                   `json:"windowOrdinal"`
	Assessor               fillerstructure.AssessorProfile       `json:"assessor"`
	MetadataSnapshotSHA256 string                                `json:"metadataSnapshotSha256"`
	PromptSHA256           string                                `json:"promptSha256"`
	SchemaSHA256           string                                `json:"schemaSha256"`
	RequestSHA256          string                                `json:"requestSha256"`
	ResponseSHA256         string                                `json:"responseSha256,omitempty"`
	StructuredOutputSHA256 string                                `json:"structuredOutputSha256,omitempty"`
	AssessmentSHA256       string                                `json:"assessmentSha256"`
	ResolvedProvider       string                                `json:"resolvedProvider,omitempty"`
	ResolvedModel          string                                `json:"resolvedModel,omitempty"`
	UpstreamProvider       string                                `json:"upstreamProvider"`
	UpstreamProviderSlug   string                                `json:"upstreamProviderSlug"`
	GenerationID           string                                `json:"generationId,omitempty"`
	Tokens                 fillerstructure.AssessmentTokenUsage  `json:"tokens"`
	RequestedNanoUSD       int64                                 `json:"requestedNanoUsd"`
	ReservedNanoUSD        int64                                 `json:"reservedNanoUsd"`
	ChargedAmountUSD       string                                `json:"chargedAmountUsd,omitempty"`
	ChargedNanoUSD         int64                                 `json:"chargedNanoUsd"`
	AccountedNanoUSD       int64                                 `json:"accountedNanoUsd"`
	ChargeKnown            bool                                  `json:"chargeKnown"`
	State                  fillerstructure.AssessmentRecordState `json:"state"`
	Failure                string                                `json:"failure,omitempty"`
	AssessedAt             time.Time                             `json:"assessedAt"`
	SHA256                 string                                `json:"sha256"`
}

// RecordedAssessment carries provider bytes and the semantic assessment only while crossing the
// persistence seam. The immutable CallRecord refers to all three by digest.
type RecordedAssessment struct {
	Record           CallRecord
	Assessment       Assessment
	RawResponse      []byte
	StructuredOutput string
}

type CallRecordInput struct {
	MediaSet               MediaSet
	WindowOrdinal          int
	Assessor               fillerstructure.AssessorProfile
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
	Tokens                 fillerstructure.AssessmentTokenUsage
	RequestedNanoUSD       int64
	ReservedNanoUSD        int64
	ChargedAmountUSD       string
	ChargedNanoUSD         int64
	AccountedNanoUSD       int64
	ChargeKnown            bool
	State                  fillerstructure.AssessmentRecordState
	Failure                string
	AssessedAt             time.Time
}
