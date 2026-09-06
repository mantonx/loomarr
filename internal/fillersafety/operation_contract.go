package fillersafety

import (
	"context"
	"errors"
	"time"
)

const (
	EvaluationReportSchemaVersion   = 1
	EvaluationReportContractVersion = "filler-spoken-safety-evaluation-report-v1"
)

var (
	// ErrEvaluationInvalid reports an invalid operation dependency or request
	// without carrying source identity, paths, or provider detail.
	ErrEvaluationInvalid = errors.New("spoken-safety evaluation: invalid")
	// ErrEvaluationIncomplete requires recovery and a new run identity. An
	// incomplete durable run is never resumed or silently replayed in place.
	ErrEvaluationIncomplete = errors.New("spoken-safety evaluation: incomplete run")
)

// EvaluationOperation is the one external spoken-safety execution seam. Its
// result is evidence only and cannot grant filler admission or ingestion.
type EvaluationOperation interface {
	Evaluate(context.Context, EvaluationRequest) (EvaluationReport, error)
}

// EvaluationRequest binds a stable attempt identity to one complete-source
// request. Source.Path is used only while the operation owns its private file
// snapshot and is excluded from every returned and durable value.
type EvaluationRequest struct {
	RunID               string
	StartedAt           time.Time
	CertificationSHA256 string
	Source              SourceRequest
}

// EvaluationReport is the canonical path-free result of one terminal run.
type EvaluationReport struct {
	SchemaVersion     int       `json:"schemaVersion"`
	ContractVersion   string    `json:"contractVersion"`
	Run               LedgerRun `json:"run"`
	Evidence          Evidence  `json:"evidence"`
	Result            Result    `json:"result"`
	TerminalEventID   string    `json:"terminalEventId"`
	TerminalEventIDs  []string  `json:"terminalEventIds"`
	TerminalCreatedAt time.Time `json:"terminalCreatedAt"`
	TerminalSHA256    string    `json:"terminalSha256"`
	SHA256            string    `json:"sha256"`
}

// HostedCallBudget carries the existing V62 spend ceilings into the domain-
// owned persistence port.
type HostedCallBudget struct {
	PerClipNanoUSD int64
	PerDayNanoUSD  int64
	PerRunNanoUSD  int64
}

// HostedCallVersions binds every semantic, media, policy, and route input
// that can change one hosted inference answer.
type HostedCallVersions struct {
	EvidenceSHA256      string
	ExtractorSHA256     string
	PromptSHA256        string
	SchemaSHA256        string
	TaxonomySHA256      string
	CertificationSHA256 string
	PolicySHA256        string
	CapabilitySHA256    string
}

// HostedCallReservation is the complete closed command that the persistence
// adapter commits with a V62 budget reservation before HTTP begins.
type HostedCallReservation struct {
	EventID, RunID, EvaluationID, ClipHash, CandidateID     string
	RequestSHA256                                           string
	Role, Rung                                              string
	RequestedProvider, RequestedModel, UpstreamProvider     string
	Modalities                                              []string
	DerivativeBytes, DerivativeDurationMS, DerivativePixels int64
	RequestedNanoUSD                                        int64
	Budget                                                  HostedCallBudget
	Versions                                                HostedCallVersions
	Ordinal                                                 int
	CreatedAt                                               time.Time
}

// HostedCallSettlement contains only bounded response authority and a closed
// failure class. It never accepts raw provider errors or response bodies.
type HostedCallSettlement struct {
	EventID, RunID, ReservationEventID                string
	ResponseSHA256                                    string
	ResolvedProvider, ResolvedModel, UpstreamProvider string
	GenerationID, Outcome                             string
	Failure                                           SettlementFailure
	ChargedAmountUSD                                  string
	ChargedNanoUSD                                    int64
	ChargeKnown                                       bool
	PromptTokens, CompletionTokens                    int64
	LatencyMS                                         int64
	Ordinal                                           int
	CreatedAt                                         time.Time
}

// ExecutionRepository is the persistence port for one evaluation. Admission
// and operator projections deliberately cannot implement it.
type ExecutionRepository interface {
	FindSpokenSafetyRun(context.Context, string) (LedgerRun, bool, error)
	BeginSpokenSafetyRun(context.Context, LedgerRun) (bool, error)
	AppendSpokenSafetyEvent(context.Context, LedgerEvent) error
	ListSpokenSafetyEvents(context.Context, string) ([]LedgerEvent, error)
	ReserveSpokenSafetyCall(context.Context, HostedCallReservation) (LedgerEvent, error)
	SettleSpokenSafetyCall(context.Context, HostedCallSettlement) (LedgerEvent, error)
}

type hostedCallIdentity struct {
	RequestedProvider, RequestedModel                 string
	ResolvedProvider, ResolvedModel, UpstreamProvider string
	CapabilitySHA256, PromptSHA256, SchemaSHA256      string
	MaxChargeNanoUSD                                  int64
}
