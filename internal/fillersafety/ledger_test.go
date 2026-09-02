package fillersafety

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLedgerContractAcceptsClosedAppendOnlyPayloads(t *testing.T) {
	hash := strings.Repeat("a", 64)
	run := LedgerRun{
		ID: "run-1", ClipHash: "clip-1", AuthoritySHA256: hash, SourceSHA256: hash,
		CertificationSHA256: hash, PolicySHA256: hash, Implementation: "spoken-safety-v1",
		SourceBytes: 1024, CreatedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
	if err := ValidateLedgerRun(run); err != nil {
		t.Fatal(err)
	}

	evidence := Evidence{ProposalState: ProposalComplete, Candidates: []Candidate{}, Audio: []AudioAssessment{}, Video: VideoNoSignal}
	events := []LedgerEvent{
		{ID: "event-plan", RunID: run.ID, Ordinal: 0, Kind: LedgerSourcePlanned,
			Source: &SourcePlanned{Audio: Span{EndMS: 1000}, Video: Span{EndMS: 1000}}, CreatedAt: run.CreatedAt},
		{ID: "event-proposal", RunID: run.ID, Ordinal: 1, Kind: LedgerProposalCompleted,
			Proposal: &ProposalCompleted{State: ProposalComplete, ProposerSHA256: hash, Candidates: []Candidate{}}, CreatedAt: run.CreatedAt},
		{ID: "event-terminal", RunID: run.ID, Ordinal: 2, Kind: LedgerTerminal,
			Terminal: &TerminalResult{Evidence: evidence, Result: Reduce(evidence), EventIDs: []string{"event-plan", "event-proposal"}}, CreatedAt: run.CreatedAt},
	}
	for _, event := range events {
		raw, err := CanonicalLedgerEvent(event)
		if err != nil {
			t.Fatalf("canonicalize %s: %v", event.Kind, err)
		}
		decoded, err := DecodeLedgerEvent(event.Kind, raw)
		if err != nil {
			t.Fatalf("decode %s: %v", event.Kind, err)
		}
		decoded.ID, decoded.RunID, decoded.Ordinal, decoded.CreatedAt = event.ID, event.RunID, event.Ordinal, event.CreatedAt
		if _, err := CanonicalLedgerEvent(decoded); err != nil {
			t.Fatalf("revalidate %s: %v", event.Kind, err)
		}
	}
}

func TestLedgerContractRejectsOpenOrSensitiveShapes(t *testing.T) {
	hash := strings.Repeat("b", 64)
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	valid := LedgerEvent{ID: "event-reserve", RunID: "run-1", Ordinal: 2, Kind: LedgerInferenceReserved,
		Reserve: &InferenceReserved{
			EvaluationID: "evaluation-1", RequestSHA256: hash, RequestedProvider: "openrouter",
			RequestedModel: "model-1", UpstreamProvider: "provider-1", CapabilitySHA256: hash,
			PromptSHA256: hash, CandidateID: "candidate-1", Modalities: []string{"audio"}, ReservedNanoUSD: 100,
		}, CreatedAt: at}

	cases := map[string]func(*LedgerEvent){
		"two payloads":         func(event *LedgerEvent) { event.Source = &SourcePlanned{Audio: Span{EndMS: 1}, Video: Span{EndMS: 1}} },
		"free form modality":   func(event *LedgerEvent) { event.Reserve.Modalities = []string{"transcript"} },
		"unsorted modalities":  func(event *LedgerEvent) { event.Reserve.Modalities = []string{"video", "audio"} },
		"path-shaped provider": func(event *LedgerEvent) { event.Reserve.RequestedProvider = "  provider  " },
		"invalid digest":       func(event *LedgerEvent) { event.Reserve.RequestSHA256 = "/private/source.mp4" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			event := valid
			reserve := *valid.Reserve
			event.Reserve = &reserve
			mutate(&event)
			if _, err := CanonicalLedgerEvent(event); !errors.Is(err, ErrLedgerInvalid) {
				t.Fatalf("error = %v, want ErrLedgerInvalid", err)
			}
		})
	}
}

func TestTerminalLedgerResultMustMatchReducer(t *testing.T) {
	evidence := Evidence{ProposalState: ProposalComplete, Candidates: []Candidate{}, Audio: []AudioAssessment{}, Video: VideoNoSignal}
	event := LedgerEvent{
		ID: "event-terminal", RunID: "run-1", Ordinal: 2, Kind: LedgerTerminal,
		Terminal:  &TerminalResult{Evidence: evidence, Result: Result{Outcome: OutcomeQuarantine, Reasons: []Reason{ReasonVideoProhibitedSignal}}, EventIDs: []string{"event-plan"}},
		CreatedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
	if _, err := CanonicalLedgerEvent(event); !errors.Is(err, ErrLedgerInvalid) {
		t.Fatalf("error = %v, want ErrLedgerInvalid", err)
	}
}
