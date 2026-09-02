package fillersafety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
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
	EvaluationID      string           `json:"evaluationId"`
	RequestSHA256     string           `json:"requestSha256"`
	RequestedProvider string           `json:"requestedProvider"`
	RequestedModel    string           `json:"requestedModel"`
	UpstreamProvider  string           `json:"upstreamProvider"`
	CapabilitySHA256  string           `json:"capabilitySha256"`
	PromptSHA256      string           `json:"promptSha256"`
	CandidateID       string           `json:"candidateId,omitempty"`
	Modalities        []string         `json:"modalities"`
	RequestedNanoUSD  int64            `json:"requestedNanoUsd"`
	ReservedNanoUSD   int64            `json:"reservedNanoUsd"`
	State             ReservationState `json:"state"`
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

func ValidateLedgerRun(run LedgerRun) error {
	if !boundedLedgerID(run.ID) || !boundedLedgerID(run.ClipHash) ||
		!validSHA256(run.AuthoritySHA256) || !validSHA256(run.SourceSHA256) ||
		!validSHA256(run.CertificationSHA256) || !validSHA256(run.PolicySHA256) || !validSHA256(run.ProposerSHA256) ||
		!boundedAuthorityID(run.Implementation) || run.SourceBytes <= 0 || run.DurationMS <= 0 || run.CreatedAt.IsZero() {
		return ErrLedgerInvalid
	}
	return nil
}

// CanonicalLedgerEvent validates an event and returns the exact payload bytes
// used for idempotency and conflict detection by every store backend.
func CanonicalLedgerEvent(event LedgerEvent) ([]byte, error) {
	if !boundedLedgerID(event.ID) || !boundedLedgerID(event.RunID) || event.Ordinal < 0 || event.CreatedAt.IsZero() {
		return nil, ErrLedgerInvalid
	}
	payloads := []any{event.Source, event.Proposal, event.Reserve, event.Settle, event.Terminal}
	present := 0
	for _, payload := range payloads {
		if !nilPayload(payload) {
			present++
		}
	}
	if present != 1 || !validEventPayload(event) {
		return nil, ErrLedgerInvalid
	}
	payload := payloadForEvent(event)
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > maxLedgerPayload {
		return nil, ErrLedgerInvalid
	}
	return raw, nil
}

func DecodeLedgerEvent(kind LedgerEventKind, raw []byte) (LedgerEvent, error) {
	event := LedgerEvent{Kind: kind}
	var target any
	switch kind {
	case LedgerSourcePlanned:
		event.Source = &SourcePlanned{}
		target = event.Source
	case LedgerProposalCompleted:
		event.Proposal = &ProposalCompleted{}
		target = event.Proposal
	case LedgerInferenceReserved:
		event.Reserve = &InferenceReserved{}
		target = event.Reserve
	case LedgerInferenceSettled:
		event.Settle = &InferenceSettled{}
		target = event.Settle
	case LedgerTerminal:
		event.Terminal = &TerminalResult{}
		target = event.Terminal
	default:
		return LedgerEvent{}, ErrLedgerInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return LedgerEvent{}, ErrLedgerInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return LedgerEvent{}, ErrLedgerInvalid
	}
	return event, nil
}

func LedgerEventInferenceID(event LedgerEvent) string {
	if event.Reserve != nil {
		return event.Reserve.EvaluationID
	}
	if event.Settle != nil {
		return event.Settle.EvaluationID
	}
	return ""
}

func validEventPayload(event LedgerEvent) bool {
	switch event.Kind {
	case LedgerSourcePlanned:
		return event.Source != nil && validCompleteSpan(event.Source.Audio) && event.Source.Audio == event.Source.Video
	case LedgerProposalCompleted:
		return event.Proposal != nil && validProposalLedger(*event.Proposal)
	case LedgerInferenceReserved:
		return event.Reserve != nil && validReservation(*event.Reserve)
	case LedgerInferenceSettled:
		return event.Settle != nil && validSettlement(*event.Settle)
	case LedgerTerminal:
		return event.Terminal != nil && validTerminal(*event.Terminal)
	default:
		return false
	}
}

func validCompleteSpan(span Span) bool { return span.StartMS == 0 && span.EndMS > 0 }

func validProposalLedger(proposal ProposalCompleted) bool {
	if !validSHA256(proposal.ProposerSHA256) ||
		(proposal.State != ProposalComplete && proposal.State != ProposalFailed) ||
		len(proposal.Candidates) > maxProposedCandidates {
		return false
	}
	if proposal.State == ProposalFailed && len(proposal.Candidates) != 0 {
		return false
	}
	for index, candidate := range proposal.Candidates {
		if !boundedLedgerID(candidate.ID) || candidate.StartMS < 0 || candidate.EndMS <= candidate.StartMS ||
			candidate.EndMS-candidate.StartMS > maxProposedIntervalMS {
			return false
		}
		if index > 0 && (proposal.Candidates[index-1].StartMS > candidate.StartMS ||
			(proposal.Candidates[index-1].StartMS == candidate.StartMS && proposal.Candidates[index-1].EndMS >= candidate.EndMS)) {
			return false
		}
	}
	return true
}

func validReservation(reservation InferenceReserved) bool {
	if !boundedLedgerID(reservation.EvaluationID) || !validSHA256(reservation.RequestSHA256) ||
		!boundedLedgerID(reservation.RequestedProvider) || !boundedLedgerID(reservation.RequestedModel) ||
		!boundedLedgerID(reservation.UpstreamProvider) || !validSHA256(reservation.CapabilitySHA256) ||
		!validSHA256(reservation.PromptSHA256) || reservation.RequestedNanoUSD < 0 || reservation.ReservedNanoUSD < 0 ||
		(reservation.CandidateID != "" && !boundedLedgerID(reservation.CandidateID)) || len(reservation.Modalities) == 0 ||
		(reservation.State != ReservationAccepted && reservation.State != ReservationHeldBudget) {
		return false
	}
	if reservation.State == ReservationAccepted && reservation.ReservedNanoUSD != reservation.RequestedNanoUSD ||
		reservation.State == ReservationHeldBudget && (reservation.RequestedNanoUSD == 0 || reservation.ReservedNanoUSD != 0) {
		return false
	}
	modalities := slices.Clone(reservation.Modalities)
	slices.Sort(modalities)
	if !slices.Equal(modalities, reservation.Modalities) || slices.ContainsFunc(modalities, func(value string) bool {
		return value != "audio" && value != "video"
	}) {
		return false
	}
	if len(slices.Compact(modalities)) != len(modalities) {
		return false
	}
	if slices.Equal(modalities, []string{"audio"}) {
		return reservation.CandidateID != ""
	}
	return reservation.CandidateID == "" && slices.Equal(modalities, []string{"audio", "video"})
}

func validSettlement(settlement InferenceSettled) bool {
	if !boundedLedgerID(settlement.ReservationEventID) || !boundedLedgerID(settlement.EvaluationID) ||
		settlement.ChargedNanoUSD < 0 || settlement.AccountedNanoUSD < 0 || settlement.PromptTokens < 0 || settlement.CompletionTokens < 0 {
		return false
	}
	switch settlement.State {
	case SettlementCompleted:
		return settlement.Failure == FailureNone && validSHA256(settlement.ResponseSHA256) &&
			boundedLedgerID(settlement.ResolvedProvider) && boundedLedgerID(settlement.ResolvedModel) &&
			boundedLedgerID(settlement.UpstreamProvider) && boundedLedgerID(settlement.GenerationID) &&
			validInferenceOutcome(settlement.Outcome) && settlement.ChargeKnown && validUSD(settlement.ChargedAmountUSD) &&
			settlement.AccountedNanoUSD == settlement.ChargedNanoUSD
	case SettlementFailed:
		return settlement.Failure != FailureInterrupted && validSettlementFailure(settlement.Failure) &&
			settlement.Outcome == "" && optionalSHA256(settlement.ResponseSHA256) &&
			optionalLedgerID(settlement.ResolvedProvider) && optionalLedgerID(settlement.ResolvedModel) &&
			optionalLedgerID(settlement.UpstreamProvider) && optionalLedgerID(settlement.GenerationID) &&
			(settlement.ChargeKnown && validUSD(settlement.ChargedAmountUSD) && settlement.AccountedNanoUSD == settlement.ChargedNanoUSD ||
				!settlement.ChargeKnown && settlement.ChargedAmountUSD == "" && settlement.ChargedNanoUSD == 0 && settlement.AccountedNanoUSD == 0)
	case SettlementUnknown:
		return settlement.Failure == FailureInterrupted && settlement.ResponseSHA256 == "" &&
			settlement.ResolvedProvider == "" && settlement.ResolvedModel == "" && settlement.UpstreamProvider == "" &&
			settlement.GenerationID == "" && settlement.Outcome == "" && !settlement.ChargeKnown &&
			settlement.ChargedAmountUSD == "" && settlement.ChargedNanoUSD == 0
	default:
		return false
	}
}

func validInferenceOutcome(value string) bool {
	switch value {
	case string(AudioDetected), string(AudioDetectedUnprojectable), string(AudioAbsent), string(AudioUnclear),
		string(AudioFailed), string(AudioInvalidResponse), string(VideoProhibited),
		string(VideoProhibitedUnprojectable), string(VideoNoSignal), string(VideoIncomplete):
		return true
	default:
		return false
	}
}

func optionalSHA256(value string) bool { return value == "" || validSHA256(value) }

func optionalLedgerID(value string) bool { return value == "" || boundedLedgerID(value) }

func validTerminal(terminal TerminalResult) bool {
	if _, valid := validateEvidence(terminal.Evidence); !valid ||
		!reflect.DeepEqual(terminal.Result, Reduce(terminal.Evidence)) || len(terminal.EventIDs) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(terminal.EventIDs))
	for _, id := range terminal.EventIDs {
		if !boundedLedgerID(id) {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func validSettlementFailure(value SettlementFailure) bool {
	switch value {
	case FailureTransport, FailureInvalidResponse, FailureRouteMismatch, FailureBudget, FailureInterrupted:
		return true
	default:
		return false
	}
}

func validUSD(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	dot := false
	for _, char := range value {
		if char == '.' && !dot {
			dot = true
			continue
		}
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != "."
}

func payloadForEvent(event LedgerEvent) any {
	switch event.Kind {
	case LedgerSourcePlanned:
		return event.Source
	case LedgerProposalCompleted:
		return event.Proposal
	case LedgerInferenceReserved:
		return event.Reserve
	case LedgerInferenceSettled:
		return event.Settle
	case LedgerTerminal:
		return event.Terminal
	default:
		return nil
	}
}

func nilPayload(value any) bool {
	switch payload := value.(type) {
	case *SourcePlanned:
		return payload == nil
	case *ProposalCompleted:
		return payload == nil
	case *InferenceReserved:
		return payload == nil
	case *InferenceSettled:
		return payload == nil
	case *TerminalResult:
		return payload == nil
	default:
		return true
	}
}

func boundedLedgerID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) || len(value) > maxLedgerIDBytes ||
		strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool { return char <= ' ' || char == 0x7f })
}

func (run LedgerRun) String() string {
	return fmt.Sprintf("spoken-safety run %s", run.ID)
}
