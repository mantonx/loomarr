package fillersafety

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestEvaluationOperationRecordsSerialCascadeBeforeReturningEvidence(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})

	report, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if report.Result.Outcome != OutcomeCandidateRejected || report.Run.ID != fixture.request.RunID ||
		fixture.proposer.Calls() != 1 || fixture.audio.Calls() != 1 || fixture.video.Calls() != 1 {
		t.Fatalf("report=%+v calls=%d/%d/%d", report, fixture.proposer.Calls(), fixture.audio.Calls(), fixture.video.Calls())
	}
	kinds := make([]LedgerEventKind, 0, len(fixture.state.events))
	for index, event := range fixture.state.events {
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
	terminal := fixture.state.events[len(fixture.state.events)-1].Terminal
	if terminal == nil || !sameResult(terminal.Result, report.Result) ||
		len(terminal.EventIDs) != len(fixture.state.events)-1 {
		t.Fatalf("terminal=%+v", terminal)
	}
	reservations := fixture.repository.Reservations()
	if len(reservations) != 2 ||
		!slices.Equal(reservations[0].Modalities, []string{"audio"}) ||
		!slices.Equal(reservations[1].Modalities, []string{"audio", "video"}) ||
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
	eventCount := len(fixture.state.events)
	second, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflectEvaluationReport(first, second) || len(fixture.state.events) != eventCount ||
		fixture.proposer.Calls() != 1 || fixture.audio.Calls() != 1 || fixture.video.Calls() != 1 {
		t.Fatalf("first=%+v second=%+v events=%d calls=%d/%d/%d", first, second, len(fixture.state.events), fixture.proposer.Calls(), fixture.audio.Calls(), fixture.video.Calls())
	}
}

func reflectEvaluationReport(first, second EvaluationReport) bool {
	return first.Run == second.Run && sameResult(first.Result, second.Result) &&
		first.Evidence.ProposalState == second.Evidence.ProposalState &&
		slices.Equal(first.Evidence.Candidates, second.Evidence.Candidates) &&
		slices.Equal(first.Evidence.Audio, second.Evidence.Audio) && first.Evidence.Video == second.Evidence.Video
}
