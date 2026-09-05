package quality_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestAcquisitionRecorderMapsTerminalStateWithoutLeakingIdentity(t *testing.T) {
	start := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	finished := start.Add(90 * time.Minute)
	for _, tc := range []struct {
		name    string
		state   provision.State
		outcome quality.Outcome
	}{
		{name: "playable", state: provision.Available, outcome: quality.OutcomePlayable},
		{name: "failed", state: provision.Unavailable, outcome: quality.OutcomeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &testkit.QualityRecorder{}
			recorder := quality.NewAcquisitionRecorder(sink, testkit.Logger())
			recorder.AcquisitionTerminal(t.Context(), provision.Record{
				Key:         "movie:tmdb:603",
				State:       tc.state,
				RequestedAt: start,
				UpdatedAt:   finished,
			})

			observations := sink.Observations()
			if len(observations) != 1 {
				t.Fatalf("observations = %+v, want one", observations)
			}
			got := observations[0]
			if got.Stage != quality.StageAcquisition || got.Outcome != tc.outcome ||
				got.At != finished || got.Duration != 90*time.Minute {
				t.Fatalf("observation = %+v", got)
			}
			if got.RunSnapshotID != "" || strings.Contains(got.IdempotencyKey, "603") || len(got.IdempotencyKey) != 64 {
				t.Fatalf("acquisition key leaked identity or attached an invented snapshot: %+v", got)
			}
		})
	}
}

func TestAcquisitionRecorderKeepsLegacyTerminalOutcomeWithUnknownDuration(t *testing.T) {
	sink := &testkit.QualityRecorder{}
	recorder := quality.NewAcquisitionRecorder(sink, testkit.Logger())
	finished := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	recorder.AcquisitionTerminal(t.Context(), provision.Record{
		Key: "series:tvdb:81189", State: provision.Available, UpdatedAt: finished,
	})

	got := sink.Observations()[0]
	if got.Outcome != quality.OutcomePlayable || got.Duration != 0 {
		t.Fatalf("legacy terminal observation = %+v", got)
	}
}

func TestAcquisitionRecorderRejectsNonterminalAndCannotFailProvisioning(t *testing.T) {
	sink := &testkit.QualityRecorder{Err: errors.New("ledger unavailable")}
	recorder := quality.NewAcquisitionRecorder(sink, testkit.Logger())
	recorder.AcquisitionTerminal(t.Context(), provision.Record{
		Key: "movie:tmdb:603", State: provision.Requested, UpdatedAt: time.Now(),
	})
	if got := sink.Observations(); len(got) != 0 {
		t.Fatalf("nonterminal acquisition recorded: %+v", got)
	}

	// The terminal method intentionally returns no error: observability cannot
	// revise the already-committed provisioning transition.
	recorder.AcquisitionTerminal(t.Context(), provision.Record{
		Key: "movie:tmdb:603", State: provision.Unavailable, UpdatedAt: time.Now(),
	})
}

func TestSchedulingRecorderUsesDistinctOpaqueFailureAndSuccessKeys(t *testing.T) {
	sink := &testkit.QualityRecorder{}
	recorder := quality.NewSchedulingRecorder(sink, testkit.Logger())
	at := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	recorder.ProposalSchedulingFailed(t.Context(), "job-private-identity", at, 3*time.Second)
	recorder.ProposalScheduled(t.Context(), "job-private-identity", at.Add(time.Minute), 2*time.Second)

	got := sink.Observations()
	if len(got) != 2 || got[0].Stage != quality.StageScheduling || got[0].Outcome != quality.OutcomeFailed ||
		got[1].Stage != quality.StageScheduling || got[1].Outcome != quality.OutcomeScheduled {
		t.Fatalf("scheduling observations = %+v", got)
	}
	if got[0].IdempotencyKey == got[1].IdempotencyKey {
		t.Fatal("failure and eventual success shared an idempotency key")
	}
	for _, observation := range got {
		if len(observation.IdempotencyKey) != 64 || strings.Contains(observation.IdempotencyKey, "job-private") ||
			observation.RunSnapshotID != "" {
			t.Fatalf("scheduling observation leaked identity or invented attribution: %+v", observation)
		}
	}
}

func TestSchedulingRecorderRejectsMissingAuthorityAndCannotFailReconcile(t *testing.T) {
	sink := &testkit.QualityRecorder{Err: errors.New("ledger unavailable")}
	recorder := quality.NewSchedulingRecorder(sink, testkit.Logger())
	recorder.ProposalScheduled(context.Background(), "", time.Now(), time.Second)
	recorder.ProposalSchedulingFailed(context.Background(), "job", time.Time{}, time.Second)
	if got := sink.Observations(); len(got) != 0 {
		t.Fatalf("invalid scheduling authority recorded: %+v", got)
	}
	recorder.ProposalScheduled(context.Background(), "job", time.Now(), time.Second)
}
