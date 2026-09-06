package fillersafety

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

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
	raw, err := json.Marshal(payloadForEvent(event))
	if err != nil || len(raw) > maxLedgerPayload {
		return nil, ErrLedgerInvalid
	}
	return raw, nil
}

// LedgerEventSHA256 binds the complete canonical event identity, not merely
// its payload, so a certification manifest can name exactly one terminal.
func LedgerEventSHA256(event LedgerEvent) (string, error) {
	payload, err := CanonicalLedgerEvent(event)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		ID        string          `json:"id"`
		RunID     string          `json:"runId"`
		Ordinal   int             `json:"ordinal"`
		Kind      LedgerEventKind `json:"kind"`
		CreatedAt string          `json:"createdAt"`
		Payload   json.RawMessage `json:"payload"`
	}{
		ID: event.ID, RunID: event.RunID, Ordinal: event.Ordinal, Kind: event.Kind,
		CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339Nano), Payload: payload,
	})
	if err != nil || len(raw) > maxLedgerPayload+maxLedgerIDBytes*2+256 {
		return "", ErrLedgerInvalid
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
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
