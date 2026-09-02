package fillersafety

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEvaluationOperationRecordsSerialCascadeBeforeReturningEvidence(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})

	report, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if report.Result.Outcome != OutcomeCandidateRejected || report.Run.ID != fixture.request.RunID ||
		report.TerminalEventID == "" || !validSHA256(report.TerminalSHA256) ||
		fixture.proposer.Calls() != 1 || fixture.audio.Calls() != 1 || fixture.video.Calls() != 1 {
		t.Fatalf("report=%+v calls=%d/%d/%d", report, fixture.proposer.Calls(), fixture.audio.Calls(), fixture.video.Calls())
	}
	events := fixture.repository.Events()
	kinds := make([]LedgerEventKind, 0, len(events))
	for index, event := range events {
		if event.Ordinal != index {
			t.Fatalf("event %d has ordinal %d", index, event.Ordinal)
		}
		kinds = append(kinds, event.Kind)
	}
	wantKinds := []LedgerEventKind{
		LedgerSourcePlanned, LedgerProposalCompleted,
		LedgerInferenceReserved, LedgerInferenceSettled,
		LedgerInferenceReserved, LedgerInferenceSettled, LedgerTerminal,
	}
	if !slices.Equal(kinds, wantKinds) {
		t.Fatalf("event kinds=%v", kinds)
	}
	terminalEvent := events[len(events)-1]
	terminal := terminalEvent.Terminal
	digest, digestErr := LedgerEventSHA256(terminalEvent)
	if terminal == nil || !sameResult(terminal.Result, report.Result) ||
		len(terminal.EventIDs) != len(events)-1 || digestErr != nil ||
		report.TerminalEventID != terminalEvent.ID || report.TerminalSHA256 != digest {
		t.Fatalf("terminal=%+v digest=%s err=%v", terminalEvent, digest, digestErr)
	}
	reservations := fixture.repository.Reservations()
	if len(reservations) != 2 ||
		!slices.Equal(reservations[0].Modalities, []string{"audio"}) ||
		!slices.Equal(reservations[1].Modalities, []string{"audio", "video"}) ||
		events[2].Reserve.Role != "spoken-safety" || events[2].Reserve.Rung != "native-audio" ||
		events[2].Reserve.DerivativeBytes <= 0 || events[2].Reserve.DerivativeDurationMS <= 0 ||
		events[4].Reserve.Rung != "complete-video" ||
		reservations[0].Versions.EvidenceSHA256 != report.Run.AuthoritySHA256 ||
		reservations[0].Versions.CertificationSHA256 != report.Run.CertificationSHA256 {
		t.Fatalf("reservations=%+v", reservations)
	}
	public, err := json.Marshal(report)
	if err != nil || strings.Contains(string(public), fixture.request.Source.Path) || strings.Contains(string(public), "complete operation source") {
		t.Fatalf("report leaked private source data: %s err=%v", public, err)
	}
}

func TestEvaluationOperationReturnsCompletedRunWithoutRepeatingWork(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	first, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	eventCount := len(fixture.repository.Events())
	second, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflectEvaluationReport(first, second) || len(fixture.repository.Events()) != eventCount ||
		fixture.proposer.Calls() != 1 || fixture.audio.Calls() != 1 || fixture.video.Calls() != 1 {
		t.Fatalf("first=%+v second=%+v events=%d calls=%d/%d/%d", first, second, len(fixture.repository.Events()), fixture.proposer.Calls(), fixture.audio.Calls(), fixture.video.Calls())
	}
}

func TestEvaluationOperationReplaysCompletedRunBeforeSourceWork(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	first, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	events, reservations, settlements := len(fixture.repository.Events()), len(fixture.repository.Reservations()), len(fixture.repository.Settlements())
	if err := os.WriteFile(fixture.request.Source.Path, []byte("changed source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflectEvaluationReport(first, second) || len(fixture.repository.Events()) != events ||
		len(fixture.repository.Reservations()) != reservations || len(fixture.repository.Settlements()) != settlements ||
		fixture.proposer.Calls() != 1 || fixture.audio.Calls() != 1 || fixture.video.Calls() != 1 {
		t.Fatalf("first=%+v second=%+v events=%d reservations=%d settlements=%d calls=%d/%d/%d", first, second, len(fixture.repository.Events()), len(fixture.repository.Reservations()), len(fixture.repository.Settlements()), fixture.proposer.Calls(), fixture.audio.Calls(), fixture.video.Calls())
	}
}

func TestEvaluationOperationDetectsHeaderConflictBeforeSourceWork(t *testing.T) {
	t.Parallel()
	mutations := map[string]func(*operationFixture){
		"source": func(fixture *operationFixture) {
			fixture.request.Source.Authority.SourceSHA256 = strings.Repeat("f", 64)
		},
		"policy": func(fixture *operationFixture) {
			fixture.request.Source.Authority.PolicySHA256 = strings.Repeat("e", 64)
		},
		"proposer": func(fixture *operationFixture) {
			fixture.operation.cascade.proposerIdentity.ConfigSHA256 = strings.Repeat("e", 64)
		},
		"started at": func(fixture *operationFixture) {
			fixture.request.StartedAt = fixture.request.StartedAt.Add(time.Second)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
			if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(fixture.request.Source.Path); err != nil {
				t.Fatal(err)
			}
			mutate(&fixture)
			if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); !errors.Is(err, ErrLedgerConflict) {
				t.Fatalf("replay error=%v, want immutable conflict", err)
			}
			if fixture.proposer.Calls() != 1 || fixture.audio.Calls() != 1 || fixture.video.Calls() != 1 {
				t.Fatalf("conflict repeated work: calls=%d/%d/%d", fixture.proposer.Calls(), fixture.audio.Calls(), fixture.video.Calls())
			}
		})
	}
}

func reflectEvaluationReport(first, second EvaluationReport) bool {
	return reflect.DeepEqual(first, second)
}
