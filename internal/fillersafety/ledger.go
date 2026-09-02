package fillersafety

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	LedgerSchemaVersion = 1
	maxLedgerIDBytes    = 128
	maxLedgerPayload    = 1 << 20
)

var (
	ErrLedgerInvalid  = errors.New("spoken-safety ledger: invalid")
	ErrLedgerConflict = errors.New("spoken-safety ledger: immutable conflict")
)

// LedgerRepository is the append-only persistence port for one spoken-safety
// execution. Admission and operator projections deliberately do not implement it.
type LedgerRepository interface {
	PutSpokenSafetyRun(context.Context, LedgerRun) error
	AppendSpokenSafetyEvent(context.Context, LedgerEvent) error
	GetSpokenSafetyRun(context.Context, string) (LedgerRun, error)
	ListSpokenSafetyEvents(context.Context, string) ([]LedgerEvent, error)
}

// LedgerRun is the immutable, path-free identity of one complete-source attempt.
type LedgerRun struct {
	ID, ClipHash, AuthoritySHA256, SourceSHA256       string
	CertificationSHA256, PolicySHA256, ProposerSHA256 string
	Implementation                                    string
	SourceBytes, DurationMS                           int64
	CreatedAt                                         time.Time
}

type LedgerEventKind string

const (
	LedgerSourcePlanned     LedgerEventKind = "source_planned"
	LedgerProposalCompleted LedgerEventKind = "proposal_completed"
	LedgerInferenceReserved LedgerEventKind = "inference_reserved"
	LedgerInferenceSettled  LedgerEventKind = "inference_settled"
	LedgerTerminal          LedgerEventKind = "terminal"
)

// LedgerEvent carries exactly one closed payload selected by Kind. No payload
// admits source text, paths, prompts, media, or free-form provider output.
type LedgerEvent struct {
	ID, RunID string
	Ordinal   int
	Kind      LedgerEventKind
	Source    *SourcePlanned
	Proposal  *ProposalCompleted
	Reserve   *InferenceReserved
	Settle    *InferenceSettled
	Terminal  *TerminalResult
	CreatedAt time.Time
}

type SourcePlanned struct {
	Audio Span `json:"audio"`
	Video Span `json:"video"`
}

type ProposalCompleted struct {
	State          ProposalState `json:"state"`
	ProposerSHA256 string        `json:"proposerSha256"`
	Candidates     []Candidate   `json:"candidates"`
}

type InferenceReserved struct {
	EvaluationID         string           `json:"evaluationId"`
	RequestSHA256        string           `json:"requestSha256"`
	RequestedProvider    string           `json:"requestedProvider"`
	RequestedModel       string           `json:"requestedModel"`
	UpstreamProvider     string           `json:"upstreamProvider"`
	Role                 string           `json:"role,omitempty"`
	Rung                 string           `json:"rung,omitempty"`
	CapabilitySHA256     string           `json:"capabilitySha256"`
	PromptSHA256         string           `json:"promptSha256"`
	SchemaSHA256         string           `json:"schemaSha256,omitempty"`
	CandidateID          string           `json:"candidateId,omitempty"`
	Modalities           []string         `json:"modalities"`
	DerivativeBytes      int64            `json:"derivativeBytes,omitempty"`
	DerivativeDurationMS int64            `json:"derivativeDurationMs,omitempty"`
	DerivativePixels     int64            `json:"derivativePixels,omitempty"`
	RequestedNanoUSD     int64            `json:"requestedNanoUsd"`
	ReservedNanoUSD      int64            `json:"reservedNanoUsd"`
	State                ReservationState `json:"state"`
}

type ReservationState string

const (
	ReservationAccepted   ReservationState = "accepted"
	ReservationHeldBudget ReservationState = "held_budget"
)

type SettlementState string

const (
	SettlementCompleted SettlementState = "completed"
	SettlementFailed    SettlementState = "failed"
	SettlementUnknown   SettlementState = "unknown"
)

type SettlementFailure string

const (
	FailureNone            SettlementFailure = ""
	FailureTransport       SettlementFailure = "transport"
	FailureInvalidResponse SettlementFailure = "invalid_response"
	FailureRouteMismatch   SettlementFailure = "route_mismatch"
	FailureBudget          SettlementFailure = "budget"
	FailureInterrupted     SettlementFailure = "interrupted"
)

type InferenceSettled struct {
	ReservationEventID string            `json:"reservationEventId"`
	EvaluationID       string            `json:"evaluationId"`
	ResponseSHA256     string            `json:"responseSha256,omitempty"`
	ResolvedProvider   string            `json:"resolvedProvider,omitempty"`
	ResolvedModel      string            `json:"resolvedModel,omitempty"`
	UpstreamProvider   string            `json:"upstreamProvider,omitempty"`
	GenerationID       string            `json:"generationId,omitempty"`
	State              SettlementState   `json:"state"`
	Failure            SettlementFailure `json:"failure"`
	Outcome            string            `json:"outcome,omitempty"`
	ChargedAmountUSD   string            `json:"chargedAmountUsd,omitempty"`
	ChargedNanoUSD     int64             `json:"chargedNanoUsd"`
	AccountedNanoUSD   int64             `json:"accountedNanoUsd"`
	ChargeKnown        bool              `json:"chargeKnown"`
	PromptTokens       int64             `json:"promptTokens"`
	CompletionTokens   int64             `json:"completionTokens"`
}

type TerminalResult struct {
	Evidence Evidence `json:"evidence"`
	Result   Result   `json:"result"`
	EventIDs []string `json:"eventIds"`
}

func (run LedgerRun) String() string {
	return fmt.Sprintf("spoken-safety run %s", run.ID)
}
