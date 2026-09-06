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
		CertificationSHA256: hash, PolicySHA256: hash, ProposerSHA256: hash, Implementation: "spoken-safety-v1",
		SourceBytes: 1024, DurationMS: 1000, CreatedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
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
			PromptSHA256: hash, CandidateID: "candidate-1", Modalities: []string{"audio"},
			RequestedNanoUSD: 100, ReservedNanoUSD: 100,
			State: ReservationAccepted,
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

func TestLedgerOpaqueIdentityPositionsRejectSourceShapes(t *testing.T) {
	hash := strings.Repeat("c", 64)
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	badIDs := []string{"/private/source.mp4", "../source.mp4", "source.mp4", "https://source.invalid/a", `source\\file`, "source id", "source\nიდენტ", "café"}

	validRun := func() LedgerRun {
		return LedgerRun{ID: "run-1", ClipHash: "clip-1", AuthoritySHA256: hash, SourceSHA256: hash,
			CertificationSHA256: hash, PolicySHA256: hash, ProposerSHA256: hash, Implementation: "spoken-safety-v1",
			SourceBytes: 1, DurationMS: 1, CreatedAt: at}
	}
	for _, field := range []struct {
		name string
		set  func(*LedgerRun, string)
	}{
		{"run", func(run *LedgerRun, value string) { run.ID = value }},
		{"clip", func(run *LedgerRun, value string) { run.ClipHash = value }},
		{"implementation", func(run *LedgerRun, value string) { run.Implementation = value }},
	} {
		for _, value := range badIDs {
			t.Run("run/"+field.name, func(t *testing.T) {
				run := validRun()
				field.set(&run, value)
				if !errors.Is(ValidateLedgerRun(run), ErrLedgerInvalid) {
					t.Fatalf("accepted %q", value)
				}
			})
		}
	}

	validEvent := func() LedgerEvent {
		return LedgerEvent{ID: "event-1", RunID: "run-1", Ordinal: 1, Kind: LedgerInferenceReserved, CreatedAt: at,
			Reserve: &InferenceReserved{EvaluationID: "evaluation-1", RequestSHA256: hash,
				RequestedProvider: "OpenRouter", RequestedModel: "openai/gpt-5-mini.2026-08-07", UpstreamProvider: "OpenAI Provider",
				CapabilitySHA256: hash, PromptSHA256: hash, CandidateID: "candidate-1", Modalities: []string{"audio"},
				RequestedNanoUSD: 1, ReservedNanoUSD: 1, State: ReservationAccepted}}
	}
	for _, field := range []struct {
		name string
		set  func(*LedgerEvent, string)
	}{
		{"event", func(event *LedgerEvent, value string) { event.ID = value }},
		{"run", func(event *LedgerEvent, value string) { event.RunID = value }},
		{"evaluation", func(event *LedgerEvent, value string) { event.Reserve.EvaluationID = value }},
		{"candidate", func(event *LedgerEvent, value string) { event.Reserve.CandidateID = value }},
	} {
		for _, value := range badIDs {
			t.Run("reservation/"+field.name, func(t *testing.T) {
				event := validEvent()
				field.set(&event, value)
				if _, err := CanonicalLedgerEvent(event); !errors.Is(err, ErrLedgerInvalid) {
					t.Fatalf("accepted %q", value)
				}
			})
		}
	}

	for _, value := range badIDs {
		t.Run("proposal/candidate", func(t *testing.T) {
			event := LedgerEvent{ID: "event-proposal", RunID: "run-1", Ordinal: 1, Kind: LedgerProposalCompleted, CreatedAt: at,
				Proposal: &ProposalCompleted{State: ProposalComplete, ProposerSHA256: hash, Candidates: []Candidate{{ID: value, EndMS: 1}}}}
			if _, err := CanonicalLedgerEvent(event); !errors.Is(err, ErrLedgerInvalid) {
				t.Fatalf("accepted %q", value)
			}
		})
		t.Run("settlement/opaque", func(t *testing.T) {
			event := LedgerEvent{ID: "event-settle", RunID: "run-1", Ordinal: 2, Kind: LedgerInferenceSettled, CreatedAt: at,
				Settle: &InferenceSettled{ReservationEventID: "reserve-1", EvaluationID: "evaluation-1", ResponseSHA256: hash,
					ResolvedProvider: "OpenRouter", ResolvedModel: "openai/gpt-5-mini.2026-08-07", UpstreamProvider: "OpenAI Provider", GenerationID: "generation-1",
					State: SettlementCompleted, Outcome: string(AudioAbsent), ChargedAmountUSD: "0", ChargeKnown: true}}
			for _, set := range []func(*InferenceSettled){
				func(settlement *InferenceSettled) { settlement.ReservationEventID = value },
				func(settlement *InferenceSettled) { settlement.EvaluationID = value },
				func(settlement *InferenceSettled) { settlement.GenerationID = value },
			} {
				copy := event
				settlement := *event.Settle
				copy.Settle = &settlement
				set(copy.Settle)
				if _, err := CanonicalLedgerEvent(copy); !errors.Is(err, ErrLedgerInvalid) {
					t.Fatalf("accepted %q", value)
				}
			}
		})
		t.Run("terminal/nested", func(t *testing.T) {
			evidence := Evidence{ProposalState: ProposalComplete, Candidates: []Candidate{{ID: value, EndMS: 1}}, Audio: []AudioAssessment{{CandidateID: value, State: AudioAbsent}}, Video: VideoNoSignal}
			event := LedgerEvent{ID: "event-terminal", RunID: "run-1", Ordinal: 2, Kind: LedgerTerminal, CreatedAt: at,
				Terminal: &TerminalResult{Evidence: evidence, Result: Reduce(evidence), EventIDs: []string{"event-1"}}}
			if _, err := CanonicalLedgerEvent(event); !errors.Is(err, ErrLedgerInvalid) {
				t.Fatalf("accepted %q", value)
			}
			evidence.Candidates[0].ID, evidence.Audio[0].CandidateID = "candidate-1", "candidate-1"
			event.Terminal.Evidence, event.Terminal.Result, event.Terminal.EventIDs = evidence, Reduce(evidence), []string{value}
			if _, err := CanonicalLedgerEvent(event); !errors.Is(err, ErrLedgerInvalid) {
				t.Fatalf("accepted event ID %q", value)
			}
		})
	}
}

func TestLedgerPublicIdentitiesKeepValidatedNamespaces(t *testing.T) {
	hash := strings.Repeat("d", 64)
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	reservation := LedgerEvent{ID: "event-reserve", RunID: "run-1", Ordinal: 1, Kind: LedgerInferenceReserved, CreatedAt: at,
		Reserve: &InferenceReserved{EvaluationID: "evaluation-1", RequestSHA256: hash,
			RequestedProvider: "OpenAI Provider", RequestedModel: "openai/gpt-5-mini.2026-08-07", UpstreamProvider: "Fournisseur Étendu",
			CapabilitySHA256: hash, PromptSHA256: hash, CandidateID: "candidate-1", Modalities: []string{"audio"},
			RequestedNanoUSD: 1, ReservedNanoUSD: 1, State: ReservationAccepted}}
	if _, err := CanonicalLedgerEvent(reservation); err != nil {
		t.Fatalf("public route labels: %v", err)
	}
	for _, value := range []string{"/private/provider", `provider\\name`, "provider\nname"} {
		copy := reservation
		payload := *reservation.Reserve
		copy.Reserve = &payload
		copy.Reserve.RequestedProvider = value
		if _, err := CanonicalLedgerEvent(copy); !errors.Is(err, ErrLedgerInvalid) {
			t.Fatalf("accepted unsafe public label %q", value)
		}
	}

	settlement := LedgerEvent{ID: "event-settle", RunID: "run-1", Ordinal: 2, Kind: LedgerInferenceSettled, CreatedAt: at,
		Settle: &InferenceSettled{ReservationEventID: "event-reserve", EvaluationID: "evaluation-1", ResponseSHA256: hash,
			ResolvedProvider: "OpenAI Provider", ResolvedModel: "openai/gpt-5-mini.2026-08-07", UpstreamProvider: "Fournisseur Étendu", GenerationID: "generation-1",
			State: SettlementCompleted, Outcome: string(AudioAbsent), ChargedAmountUSD: "0", ChargeKnown: true}}
	if _, err := CanonicalLedgerEvent(settlement); err != nil {
		t.Fatalf("public response labels: %v", err)
	}
	failed := settlement
	failed.Settle = &InferenceSettled{ReservationEventID: "event-reserve", EvaluationID: "evaluation-1",
		ResolvedProvider: "OpenAI Provider", ResolvedModel: "openai/gpt-5-mini.2026-08-07", UpstreamProvider: "Fournisseur Étendu",
		State: SettlementFailed, Failure: FailureTransport}
	if _, err := CanonicalLedgerEvent(failed); err != nil {
		t.Fatalf("optional failed response labels: %v", err)
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
