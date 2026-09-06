package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	CallReservationSchemaVersion   = 2
	CallReservationContractVersion = "filler-structure-window-call-reservation-v2"
)

type CallReservationState string

const (
	CallReservationAccepted   CallReservationState = "accepted"
	CallReservationHeldBudget CallReservationState = "held_budget"
)

type CallLedgerState string

const (
	CallLedgerOpen       CallLedgerState = "open"
	CallLedgerHeldBudget CallLedgerState = "held_budget"
	CallLedgerSettled    CallLedgerState = "settled"
)

var ErrCallLedgerConflict = errors.New("structure window call ledger conflicts with existing authority")

type CallReservation struct {
	SchemaVersion          int                             `json:"schemaVersion"`
	ContractVersion        string                          `json:"contractVersion"`
	RequestSHA256          string                          `json:"requestSha256"`
	MediaSet               MediaSet                        `json:"mediaSet"`
	WindowOrdinal          int                             `json:"windowOrdinal"`
	Assessor               fillerstructure.AssessorProfile `json:"assessor"`
	MetadataSnapshotSHA256 string                          `json:"metadataSnapshotSha256"`
	PromptSHA256           string                          `json:"promptSha256"`
	SchemaSHA256           string                          `json:"schemaSha256"`
	ExpectedResolvedModel  string                          `json:"expectedResolvedModel"`
	UpstreamProvider       string                          `json:"upstreamProvider"`
	UpstreamProviderSlug   string                          `json:"upstreamProviderSlug"`
	RequestedNanoUSD       int64                           `json:"requestedNanoUsd"`
	MaximumChargeNanoUSD   int64                           `json:"maximumChargeNanoUsd"`
	RequestedAt            time.Time                       `json:"requestedAt"`
	SHA256                 string                          `json:"sha256"`
}

type CallReservationInput struct {
	RequestSHA256          string
	MediaSet               MediaSet
	WindowOrdinal          int
	Assessor               fillerstructure.AssessorProfile
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

type CallLedgerEntry struct {
	Reservation CallReservation `json:"reservation"`
	State       CallLedgerState `json:"state"`
	Record      *CallRecord     `json:"record,omitempty"`
}

func NewCallReservation(input CallReservationInput) (CallReservation, error) {
	reservation := CallReservation{
		SchemaVersion: CallReservationSchemaVersion, ContractVersion: CallReservationContractVersion,
		RequestSHA256: input.RequestSHA256, MediaSet: input.MediaSet, WindowOrdinal: input.WindowOrdinal,
		Assessor: input.Assessor, MetadataSnapshotSHA256: input.MetadataSnapshotSHA256,
		PromptSHA256: input.PromptSHA256, SchemaSHA256: input.SchemaSHA256,
		ExpectedResolvedModel: input.ExpectedResolvedModel, UpstreamProvider: input.UpstreamProvider,
		UpstreamProviderSlug: input.UpstreamProviderSlug, RequestedNanoUSD: input.RequestedNanoUSD,
		MaximumChargeNanoUSD: input.MaximumChargeNanoUSD, RequestedAt: input.RequestedAt.UTC().Round(0),
	}
	reservation.SHA256 = CallReservationSHA256(reservation)
	return reservation, ValidateCallReservation(reservation)
}

func CallReservationSHA256(reservation CallReservation) string {
	reservation.SHA256 = ""
	raw, err := json.Marshal(reservation)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
