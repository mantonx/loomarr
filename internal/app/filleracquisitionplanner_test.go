package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestExistingRemoteStates_DistinguishesCommittedAndDeclinedCandidates(t *testing.T) {
	queued := filler.PullPlanRow{SourceID: "classic", Provider: "archive", RemoteID: "queued"}
	dropped := filler.PullPlanRow{SourceID: "classic", Provider: "archive", RemoteID: "dropped", Dropped: true}
	dismissed := filler.PullPlanRow{SourceID: "classic", Provider: "archive", RemoteID: "dismissed"}
	intent := filler.AcquisitionIntent{EraStart: 1990, EraEnd: 1999}
	states := existingRemoteStates([]filler.Pull{
		{Status: filler.PullApproved, Intent: intent, Plan: []filler.PullPlanRow{queued, dropped}},
		{Status: filler.PullDismissed, Intent: intent, Plan: []filler.PullPlanRow{dismissed}},
	}, intent)
	if states[queued.Identity().Key()] != filler.RemoteQueued {
		t.Fatalf("committed candidate = %q, want queued", states[queued.Identity().Key()])
	}
	for _, row := range []filler.PullPlanRow{dropped, dismissed} {
		if states[row.Identity().Key()] != filler.RemoteDeclined {
			t.Fatalf("declined candidate %s = %q, want declined", row.RemoteID, states[row.Identity().Key()])
		}
	}
}

func TestExistingRemoteStates_DeclinesOnlyWithinTheSameIntentFamily(t *testing.T) {
	row := filler.PullPlanRow{SourceID: "classic", Provider: "archive", RemoteID: "declined"}
	original := filler.AcquisitionIntent{EraStart: 1990, EraEnd: 1999, Count: 1, CatalogReason: "first reason"}
	sameFamily := filler.AcquisitionIntent{EraStart: 1990, EraEnd: 1999, Count: 50, CatalogReason: "another reason"}
	differentFamily := filler.AcquisitionIntent{EraStart: 1980, EraEnd: 1989}
	pulls := []filler.Pull{{Status: filler.PullDismissed, Intent: original, Plan: []filler.PullPlanRow{row}}}
	if got := existingRemoteStates(pulls, sameFamily)[row.Identity().Key()]; got != filler.RemoteDeclined {
		t.Fatalf("same-family state = %q, want declined", got)
	}
	if got := existingRemoteStates(pulls, differentFamily)[row.Identity().Key()]; got != "" {
		t.Fatalf("different-family state = %q, want candidate reconsidered", got)
	}
}

type enumeratorFunc func(context.Context, filler.FetchSource, int) ([]filler.DiscoveredRef, int, error)

func (f enumeratorFunc) Enumerate(ctx context.Context, source filler.FetchSource, limit int) ([]filler.DiscoveredRef, int, error) {
	return f(ctx, source, limit)
}

func TestPlanAcquisition_BoundsTheWholeSourceAndCandidateSet(t *testing.T) {
	sources := make([]store.FillerSource, maxAcquisitionPlanningSources+2)
	for i := range sources {
		sources[i] = store.NewFillerSource(fmt.Sprintf("source-%02d", i), "archive", fmt.Sprintf("https://example.test/collection-%02d", i), "", time.Unix(int64(i), 0))
	}
	st := testkit.MigratedSQLiteStore(t)
	for _, source := range sources {
		if err := st.UpsertFillerSource(t.Context(), source); err != nil {
			t.Fatal(err)
		}
	}
	testSourceIDs := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		testSourceIDs[source.ID] = struct{}{}
	}
	registered, err := st.ListFillerSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range registered {
		if _, isTestSource := testSourceIDs[source.ID]; !isTestSource {
			if err := st.SetFillerSourceEnabled(t.Context(), source.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	var totalLimit, deadlines int
	enumerator := enumeratorFunc(func(ctx context.Context, source filler.FetchSource, limit int) ([]filler.DiscoveredRef, int, error) {
		totalLimit += limit
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= acquisitionPlanningTimeout {
			deadlines++
		}
		items := make([]filler.DiscoveredRef, limit)
		for i := range items {
			id := fmt.Sprintf("%s-%03d", source.ID, i)
			items[i] = filler.DiscoveredRef{ID: id, URL: "https://example.test/" + id}
		}
		return items, limit, nil
	})
	adapter := fillerServiceAdapter{
		pullPlanning: st,
		sourceEnum:   enumerator,
	}
	plan, err := adapter.PlanAcquisition(t.Context(), filler.AcquisitionIntent{Count: 50})
	if err != nil {
		t.Fatal(err)
	}
	if totalLimit != maxAcquisitionPlanningCandidates {
		t.Fatalf("enumeration budget = %d, want %d total candidates", totalLimit, maxAcquisitionPlanningCandidates)
	}
	if deadlines != maxAcquisitionPlanningSources {
		t.Fatalf("bounded calls = %d, want %d calls carrying the planning deadline", deadlines, maxAcquisitionPlanningSources)
	}
	limited := 0
	for _, decision := range plan.Sources {
		if decision.Disposition == filler.AcquisitionSourceLimitExceeded {
			limited++
		}
	}
	if limited != 2 || len(plan.Selected) != 50 {
		t.Fatalf("source decisions limited=%d selected=%d, want 2 and 50", limited, len(plan.Selected))
	}
}
