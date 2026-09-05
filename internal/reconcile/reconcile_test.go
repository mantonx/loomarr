package reconcile

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
	"github.com/loomarr/loomarr/internal/testkit/libraryfixture"
)

var now = time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*Reconciler, store.Store, *testkit.Requester, *testkit.MediaServer) {
	t.Helper()
	st, err := store.Open(context.Background(), "sqlite://"+t.TempDir()+"/r.db", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	req := &testkit.Requester{}
	ms := testkit.NewMediaServer(t)
	t.Cleanup(ms.Close)
	lib := library.New(library.Emby, ms.URL, ms.AdminToken, "dev")
	rc := New(st, req, lib, nil, Config{
		RequestTTL: 48 * time.Hour, DownloadingTTL: 12 * time.Hour, Batch: 50, Lease: 2 * time.Minute,
	}, func() time.Time { return now }, slog.New(slog.DiscardHandler))
	return rc, st, req, ms
}

// captureEmitter records the domain events the reconciler fans out, so the #10
// seam (reconciler terminal transition → scheduler/SSE feed) is asserted.
type captureEmitter struct{ events []provision.DomainEvent }

func (c *captureEmitter) Emit(_ context.Context, ev provision.DomainEvent) {
	c.events = append(c.events, ev)
}

// setupWithEmitter is setup() with a capturing emitter wired in.
func setupWithEmitter(t *testing.T) (*Reconciler, store.Store, *testkit.MediaServer, *captureEmitter) {
	t.Helper()
	st, err := store.Open(context.Background(), "sqlite://"+t.TempDir()+"/r.db", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	req := &testkit.Requester{}
	ms := testkit.NewMediaServer(t)
	t.Cleanup(ms.Close)
	lib := library.New(library.Emby, ms.URL, ms.AdminToken, "dev")
	emit := &captureEmitter{}
	rc := New(st, req, lib, emit, Config{
		RequestTTL: 48 * time.Hour, DownloadingTTL: 12 * time.Hour, Batch: 50, Lease: 2 * time.Minute,
	}, func() time.Time { return now }, slog.New(slog.DiscardHandler))
	return rc, st, ms, emit
}

func put(t *testing.T, st store.Store, key provision.Key, state provision.State, tmdb int, deadline time.Time) {
	t.Helper()
	err := st.UpsertTitle(context.Background(), provision.Record{
		Key: key, State: state, Deadline: deadline,
		Title: provision.Title{MediaType: provision.Movie, TMDBID: tmdb},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A due `wanted` title gets resubmitted; success → requested.
func TestRetryWantedSuccess(t *testing.T) {
	rc, st, req, _ := setup(t)
	put(t, st, "movie:tmdb:1", provision.Wanted, 1, now.Add(-time.Hour))

	n, err := rc.Tick(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Tick = %d,%v want 1,nil", n, err)
	}
	if req.RequestCount() != 1 {
		t.Errorf("expected one resubmit, got %d", req.RequestCount())
	}
	rec, _ := st.GetTitle(context.Background(), "movie:tmdb:1")
	if rec.State != provision.Requested {
		t.Errorf("wanted+success → %s, want requested", rec.State)
	}
	if !rec.Deadline.After(now) {
		t.Errorf("resubmit should set a future deadline, got %v", rec.Deadline)
	}
}

// A due `wanted` title whose resubmit fails stays wanted (reconciler retries).
func TestRetryWantedFailureStaysWanted(t *testing.T) {
	rc, st, req, _ := setup(t)
	req.RequestErr = errors.New("seerr down")
	put(t, st, "movie:tmdb:2", provision.Wanted, 2, now.Add(-time.Hour))

	if _, err := rc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, _ := st.GetTitle(context.Background(), "movie:tmdb:2")
	if rec.State != provision.Wanted {
		t.Errorf("failed resubmit → %s, want wanted", rec.State)
	}
	if !rec.Deadline.After(now) {
		t.Error("failed resubmit should push the deadline out for a later retry")
	}
}

// Missed-webhook backstop: a due `requested` title the library now confirms →
// available (even though no Import webhook arrived).
func TestMissedWebhookRecheckToAvailable(t *testing.T) {
	rc, st, _, ms := setup(t)
	ms.PresentTMDB = "16153" // library confirms this title
	put(t, st, "movie:tmdb:16153", provision.Requested, 16153, now.Add(-time.Hour))

	if _, err := rc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, _ := st.GetTitle(context.Background(), "movie:tmdb:16153")
	if rec.State != provision.Available {
		t.Errorf("missed-webhook re-check with library present → %s, want available", rec.State)
	}
	if rec.LibraryID == "" {
		t.Error("confirmed record should carry the library id")
	}
}

// Deadline give-up: a due in-flight title the library does NOT confirm →
// unavailable + best-effort Cancel.
func TestDeadlineGiveUp(t *testing.T) {
	rc, st, req, _ := setup(t)
	put(t, st, "movie:tmdb:404", provision.Downloading, 404, now.Add(-time.Hour)) // library absent for 404

	if _, err := rc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, _ := st.GetTitle(context.Background(), "movie:tmdb:404")
	if rec.State != provision.Unavailable {
		t.Errorf("past-deadline unconfirmed → %s, want unavailable", rec.State)
	}
	if req.CancelCount() != 1 {
		t.Errorf("give-up should best-effort Cancel, got %d cancels", req.CancelCount())
	}
}

func TestReconcilerRecordsTerminalAcquisitionQualityAfterCommit(t *testing.T) {
	rc, st, req, ms := setup(t)
	sink := &testkit.QualityRecorder{}
	rc.WithQualityRecorder(quality.NewAcquisitionRecorder(sink, testkit.Logger()))

	requestedAt := now.Add(-2 * time.Hour)
	available := provision.Record{
		Key: "movie:tmdb:16153", State: provision.Requested, RequestedAt: requestedAt,
		Deadline: now.Add(-time.Hour), Title: provision.Title{MediaType: provision.Movie, TMDBID: 16153},
	}
	if err := st.UpsertTitle(t.Context(), available); err != nil {
		t.Fatal(err)
	}
	ms.PresentTMDB = "16153"
	if _, err := rc.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}

	failed := provision.Record{
		Key: "movie:tmdb:404", State: provision.Downloading, RequestedAt: requestedAt,
		Deadline: now.Add(-time.Hour), Title: provision.Title{MediaType: provision.Movie, TMDBID: 404},
	}
	if err := st.UpsertTitle(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	ms.PresentTMDB = ""
	if _, err := rc.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}

	got := sink.Observations()
	if len(got) != 2 || got[0].Outcome != quality.OutcomePlayable || got[1].Outcome != quality.OutcomeFailed {
		t.Fatalf("terminal acquisition observations = %+v", got)
	}
	for _, observation := range got {
		if observation.Stage != quality.StageAcquisition || observation.At != now || observation.Duration != 2*time.Hour {
			t.Fatalf("acquisition observation = %+v", observation)
		}
	}
	if req.CancelCount() != 1 {
		t.Fatalf("quality recording changed provisioning: cancels = %d, want 1", req.CancelCount())
	}
}

func TestTickSnapshotsLibraryOnceAcrossClaimedBatch(t *testing.T) {
	st := testkit.SQLiteStore(t)
	primary := libraryfixture.NewLookup(map[string]libraryfixture.LookupResult{
		"101": {ItemID: "item-101", Present: true},
		"202": {ItemID: "item-202", Present: true},
	})
	rotated := libraryfixture.NewLookup(nil)
	snapshots := 0
	rc := NewDynamic(st, &testkit.Requester{}, func() LibraryLookup {
		snapshots++
		if snapshots == 1 {
			return primary
		}
		return rotated
	}, nil, Config{Batch: 50, Lease: 2 * time.Minute}, func() time.Time { return now }, slog.New(slog.DiscardHandler))
	put(t, st, "movie:tmdb:101", provision.Requested, 101, now.Add(-time.Hour))
	put(t, st, "movie:tmdb:202", provision.Requested, 202, now.Add(-time.Hour))

	if count, err := rc.Tick(context.Background()); err != nil || count != 2 {
		t.Fatalf("Tick = %d, %v; want 2, nil", count, err)
	}
	if snapshots != 1 {
		t.Fatalf("library snapshots = %d, want one for the claimed batch", snapshots)
	}
	if calls := primary.Calls(); len(calls) != 2 {
		t.Fatalf("primary lookup calls = %v, want both claimed titles", calls)
	}
	if calls := rotated.Calls(); len(calls) != 0 {
		t.Fatalf("rotated library received in-flight batch calls: %v", calls)
	}
}

// TestReconcilerEmitsTerminalEvents is the reconciler half of the #10 seam: the
// reconciler's own terminal transitions (missed-webhook re-check → available;
// deadline give-up → unavailable) must fan domain events to the emitter, so the
// scheduler backfills and the UI updates without an Import webhook ever arriving.
// A non-terminal Tick (retry a wanted title) must emit nothing.
func TestReconcilerEmitsTerminalEvents(t *testing.T) {
	rc, st, ms, emit := setupWithEmitter(t)

	// A wanted retry is non-terminal (→ requested) → no availability event.
	put(t, st, "movie:tmdb:1", provision.Wanted, 1, now.Add(-time.Hour))
	if _, err := rc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(emit.events) != 0 {
		t.Fatalf("wanted→requested emitted %d events, want 0 (non-terminal)", len(emit.events))
	}

	// Missed-webhook re-check confirms the library → available (terminal → event).
	ms.PresentTMDB = "16153"
	put(t, st, "movie:tmdb:16153", provision.Requested, 16153, now.Add(-time.Hour))
	if _, err := rc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(emit.events) != 1 {
		t.Fatalf("missed-webhook confirm emitted %d events, want 1 (available)", len(emit.events))
	}
	if ev := emit.events[0]; ev.State != provision.Available || ev.Key != "movie:tmdb:16153" {
		t.Errorf("emitted %v, want {movie:tmdb:16153, available}", ev)
	}

	// Deadline give-up on an unconfirmed title → unavailable (terminal → event).
	put(t, st, "movie:tmdb:404", provision.Downloading, 404, now.Add(-time.Hour))
	if _, err := rc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(emit.events) != 2 {
		t.Fatalf("after give-up: %d events total, want 2 (available + unavailable)", len(emit.events))
	}
	if ev := emit.events[1]; ev.State != provision.Unavailable || ev.Key != "movie:tmdb:404" {
		t.Errorf("emitted %v, want {movie:tmdb:404, unavailable}", ev)
	}
}

// A not-yet-due title isn't claimed, so the reconciler leaves it alone.
func TestNotDueUntouched(t *testing.T) {
	rc, st, req, _ := setup(t)
	put(t, st, "movie:tmdb:5", provision.Requested, 5, now.Add(time.Hour)) // future deadline

	n, _ := rc.Tick(context.Background())
	if n != 0 {
		t.Errorf("claimed %d not-due titles, want 0", n)
	}
	if req.RequestCount() != 0 || req.CancelCount() != 0 {
		t.Error("not-due title should not be acted on")
	}
}

// Terminal titles are never claimed even if their (stale) deadline is past.
func TestTerminalNotClaimed(t *testing.T) {
	rc, st, _, _ := setup(t)
	put(t, st, "movie:tmdb:6", provision.Available, 6, now.Add(-time.Hour))
	put(t, st, "movie:tmdb:7", provision.Unavailable, 7, now.Add(-time.Hour))

	n, _ := rc.Tick(context.Background())
	if n != 0 {
		t.Errorf("claimed %d terminal titles, want 0", n)
	}
}
