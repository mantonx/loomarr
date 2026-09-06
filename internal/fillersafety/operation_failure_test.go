package fillersafety

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/openroutermedia"
)

func TestEvaluationOperationBudgetHoldPreventsHTTPAndReturnsDurableHold(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	fixture.state.budgetHeld = true

	report, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if report.Result.Outcome != OutcomeHold || fixture.audio.Calls() != 0 || fixture.video.Calls() != 0 ||
		len(fixture.repository.Reservations()) != 1 || len(fixture.repository.Settlements()) != 0 {
		t.Fatalf("report=%+v calls=%d/%d reservations=%d settlements=%d", report, fixture.audio.Calls(), fixture.video.Calls(), len(fixture.repository.Reservations()), len(fixture.repository.Settlements()))
	}
	if events := fixture.repository.Events(); events[len(events)-1].Terminal == nil {
		t.Fatalf("budget hold did not reach a durable terminal: %+v", events[len(events)-1])
	}
}

func TestEvaluationOperationDistinguishesPinnedRouteMismatch(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	fixture.audioResult.err = openroutermedia.ErrRouteMismatch

	report, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	settlements := fixture.repository.Settlements()
	if report.Result.Outcome != OutcomeHold || len(settlements) != 1 ||
		settlements[0].Failure != FailureRouteMismatch ||
		settlements[0].ResolvedProvider != "" || settlements[0].ResolvedModel != "" {
		t.Fatalf("report=%+v settlement=%+v", report, settlements)
	}
}

func TestEvaluationOperationPersistsUnprojectablePresenceWithoutPromotingIt(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, nil)
	fixture.videoResult.state = VideoProhibitedUnprojectable
	fixture.videoResult.err = errors.New("private malformed timing detail")

	report, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if report.Result.Outcome != OutcomeHold || !slices.Equal(report.Result.Reasons, []Reason{ReasonPresenceUnprojectable}) ||
		len(fixture.repository.Settlements()) != 1 || fixture.repository.Settlements()[0].Failure != FailureInvalidResponse {
		t.Fatalf("report=%+v settlements=%+v", report, fixture.repository.Settlements())
	}
	raw, err := json.Marshal(fixture.repository.Events())
	if err != nil || strings.Contains(string(raw), "private malformed timing detail") {
		t.Fatalf("ledger retained private provider detail: %s err=%v", raw, err)
	}
}

func TestEvaluationOperationReservationFailureLeavesRunForRecovery(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	fixture.state.reserveErr = errors.New("private persistence detail")

	if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); err == nil {
		t.Fatal("expected reservation persistence failure")
	}
	if events := fixture.repository.Events(); fixture.audio.Calls() != 0 || len(events) != 2 || events[1].Proposal == nil {
		t.Fatalf("calls=%d events=%+v", fixture.audio.Calls(), events)
	}
	if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); !errors.Is(err, ErrEvaluationIncomplete) {
		t.Fatalf("in-place retry=%v, want ErrEvaluationIncomplete", err)
	}
}

func TestEvaluationOperationPlanningFailureLeavesIncompleteHeader(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	if err := os.Remove(fixture.request.Source.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); err == nil {
		t.Fatal("expected planning failure")
	}
	if !fixture.repository.Begun() || len(fixture.repository.Events()) != 0 || fixture.proposer.Calls() != 0 || fixture.audio.Calls() != 0 || fixture.video.Calls() != 0 {
		t.Fatalf("header=%t events=%d calls=%d/%d/%d", fixture.repository.Begun(), len(fixture.repository.Events()), fixture.proposer.Calls(), fixture.audio.Calls(), fixture.video.Calls())
	}
	if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); !errors.Is(err, ErrEvaluationIncomplete) {
		t.Fatalf("duplicate error=%v, want ErrEvaluationIncomplete", err)
	}
}

func TestEvaluationOperationUnknownChargeLeavesAcceptedReservationUnsettled(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	fixture.audioResult.err = errors.New("private transport detail")
	fixture.audioResult.unknownCharge = true

	if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); !errors.Is(err, ErrEvaluationIncomplete) {
		t.Fatalf("err=%v, want ErrEvaluationIncomplete", err)
	}
	events := fixture.repository.Events()
	if fixture.audio.Calls() != 1 || len(fixture.repository.Settlements()) != 0 ||
		len(events) != 3 || events[2].Reserve == nil {
		t.Fatalf("calls=%d settlements=%d events=%+v", fixture.audio.Calls(), len(fixture.repository.Settlements()), events)
	}
}

func TestEvaluationOperationSettlementFailureCannotReturnUnrecordedEvidence(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	fixture.state.settleErr = errors.New("private settlement detail")

	if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); err == nil {
		t.Fatal("expected settlement persistence failure")
	}
	if events := fixture.repository.Events(); fixture.audio.Calls() != 1 || len(events) != 3 ||
		events[len(events)-1].Reserve == nil {
		t.Fatalf("calls=%d events=%+v", fixture.audio.Calls(), events)
	}
}
