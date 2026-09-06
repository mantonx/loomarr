package fillerstructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const (
	AssessmentReservationSchemaVersion   = 4
	AssessmentReservationContractVersion = "filler-structure-assessment-reservation-v4"
)

type AssessmentReservationState string

const (
	AssessmentReservationAccepted   AssessmentReservationState = "accepted"
	AssessmentReservationHeldBudget AssessmentReservationState = "held_budget"
)

type AssessmentLedgerState string

const (
	AssessmentLedgerOpen       AssessmentLedgerState = "open"
	AssessmentLedgerHeldBudget AssessmentLedgerState = "held_budget"
	AssessmentLedgerSettled    AssessmentLedgerState = "settled"
)

type AssessmentReservation struct {
	SchemaVersion          int             `json:"schemaVersion"`
	ContractVersion        string          `json:"contractVersion"`
	RequestSHA256          string          `json:"requestSha256"`
	Source                 Source          `json:"source"`
	Media                  AssessmentMedia `json:"media"`
	Assessor               AssessorProfile `json:"assessor"`
	MetadataSnapshotSHA256 string          `json:"metadataSnapshotSha256"`
	PromptSHA256           string          `json:"promptSha256"`
	SchemaSHA256           string          `json:"schemaSha256"`
	ExpectedResolvedModel  string          `json:"expectedResolvedModel"`
	UpstreamProvider       string          `json:"upstreamProvider"`
	UpstreamProviderSlug   string          `json:"upstreamProviderSlug"`
	RequestedNanoUSD       int64           `json:"requestedNanoUsd"`
	MaximumChargeNanoUSD   int64           `json:"maximumChargeNanoUsd"`
	RequestedAt            time.Time       `json:"requestedAt"`
	SHA256                 string          `json:"sha256"`
}

type AssessmentReservationInput struct {
	RequestSHA256          string
	Source                 Source
	Media                  AssessmentMedia
	Assessor               AssessorProfile
	MetadataSnapshotSHA256 string
	PromptSHA256           string
	SchemaSHA256           string
	ExpectedResolvedModel  string
	UpstreamProvider       string
	UpstreamProviderSlug   string
	RequestedNanoUSD       int64
	MaximumChargeNanoUSD   int64
	RequestedAt            time.Time
}

type AssessmentLedgerEntry struct {
	Reservation AssessmentReservation `json:"reservation"`
	State       AssessmentLedgerState `json:"state"`
	Record      *AssessmentRecord     `json:"record,omitempty"`
}

func NewAssessmentReservation(input AssessmentReservationInput) (AssessmentReservation, error) {
	reservation := AssessmentReservation{
		SchemaVersion: AssessmentReservationSchemaVersion, ContractVersion: AssessmentReservationContractVersion,
		RequestSHA256: input.RequestSHA256, Source: input.Source, Media: input.Media,
		Assessor: input.Assessor, MetadataSnapshotSHA256: input.MetadataSnapshotSHA256,
		PromptSHA256: input.PromptSHA256, SchemaSHA256: input.SchemaSHA256,
		ExpectedResolvedModel: input.ExpectedResolvedModel,
		UpstreamProvider:      input.UpstreamProvider, UpstreamProviderSlug: input.UpstreamProviderSlug,
		RequestedNanoUSD: input.RequestedNanoUSD, MaximumChargeNanoUSD: input.MaximumChargeNanoUSD,
		RequestedAt: input.RequestedAt.UTC().Round(0),
	}
	reservation.SHA256 = AssessmentReservationSHA256(reservation)
	return reservation, ValidateAssessmentReservation(reservation)
}

func AssessmentReservationSHA256(reservation AssessmentReservation) string {
	reservation.SHA256 = ""
	raw, err := json.Marshal(reservation)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
