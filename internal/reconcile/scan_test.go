package reconcile

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

// setupScan wires a LibraryScan over a real store + testkit media server (whose SearchItems
// declare "what's in the library") + a capturing emitter, mirroring setupWithEmitter.
func setupScan(t *testing.T) (*LibraryScan, store.Store, *testkit.MediaServer, *captureEmitter) {
	t.Helper()
	st, err := store.Open(context.Background(), "sqlite://"+t.TempDir()+"/scan.db", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ms := testkit.NewMediaServer(t)
	t.Cleanup(ms.Close)
	lib := library.New(library.Emby, ms.URL, ms.AdminToken, "dev")
	emit := &captureEmitter{}
	ls := NewLibraryScan(st, lib, emit, time.Hour, func() time.Time { return now }, slog.New(slog.DiscardHandler))
	return ls, st, ms, emit
}

func getState(t *testing.T, st store.Store, key provision.Key) provision.State {
	t.Helper()
	rec, err := st.GetTitle(context.Background(), key)
	if err != nil {
		t.Fatalf("GetTitle %s: %v", key, err)
	}
	return rec.State
}

// A requested movie that shows up in the library's recently-added is confirmed → available,
// with the LibraryID stamped and a single terminal event emitted.
func TestLibraryScan_ConfirmsRequestedMovie(t *testing.T) {
	ls, st, ms, emit := setupScan(t)
	key := provision.Key("movie:tmdb:603")
	put(t, st, key, provision.Requested, 603, now.Add(24*time.Hour))
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-1", Name: "The Matrix", Type: "Movie", TMDBID: 603},
	}

	n, err := ls.Incremental(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("confirmed %d, want 1", n)
	}
	if got := getState(t, st, key); got != provision.Available {
		t.Errorf("state = %s, want available", got)
	}
	rec, _ := st.GetTitle(context.Background(), key)
	if rec.LibraryID != "lib-1" {
		t.Errorf("LibraryID = %q, want lib-1", rec.LibraryID)
	}
	if len(emit.events) != 1 || emit.events[0].State != provision.Available {
		t.Errorf("events = %+v, want one available event", emit.events)
	}
}

func TestLibraryScanRecordsPlayableQualityAfterCommittedConfirmation(t *testing.T) {
	ls, st, ms, _ := setupScan(t)
	sink := &testkit.QualityRecorder{}
	ls.WithQualityRecorder(quality.NewAcquisitionRecorder(sink, testkit.Logger()))
	key := provision.Key("movie:tmdb:603")
	if err := st.UpsertTitle(t.Context(), provision.Record{
		Key: key, State: provision.Requested, RequestedAt: now.Add(-45 * time.Minute),
		Deadline: now.Add(24 * time.Hour), Title: provision.Title{MediaType: provision.Movie, TMDBID: 603},
	}); err != nil {
		t.Fatal(err)
	}
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-1", Name: "The Matrix", Type: "Movie", TMDBID: 603},
	}

	if n, err := ls.Incremental(t.Context()); err != nil || n != 1 {
		t.Fatalf("Incremental = %d, %v", n, err)
	}
	got := sink.Observations()
	if len(got) != 1 || got[0].Stage != quality.StageAcquisition ||
		got[0].Outcome != quality.OutcomePlayable || got[0].Duration != 45*time.Minute {
		t.Fatalf("quality observations = %+v", got)
	}
}

// A downloading series correlates on its TVDB key (series:tvdb:<id>), proving key parity holds
// across the series-prefers-tvdb path.
func TestLibraryScan_ConfirmsSeriesByTVDB(t *testing.T) {
	ls, st, ms, _ := setupScan(t)
	key := provision.Key("series:tvdb:81189")
	if err := st.UpsertTitle(context.Background(), provision.Record{
		Key: key, State: provision.Downloading, Deadline: now.Add(12 * time.Hour),
		Title: provision.Title{MediaType: provision.Series, TVDBID: 81189},
	}); err != nil {
		t.Fatal(err)
	}
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-2", Name: "Breaking Bad", Type: "Series", TVDBID: 81189},
	}

	n, err := ls.Incremental(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("confirmed %d, want 1", n)
	}
	if got := getState(t, st, key); got != provision.Available {
		t.Errorf("state = %s, want available", got)
	}
}

// The Bluey scenario: a series was added TMDB-only (keyed series:tmdb:<id>, no TVDB id yet —
// the suggester/channel-add path), and its episodes land in the library, which indexes the show
// with BOTH a TVDB and a TMDB id. The scan must still confirm it available by matching on the
// item's TMDB id, even though Title.Key() prefers the item's TVDB id. Without ScanItemKeys
// probing every id, a TMDB-keyed series is stuck `requested`/`downloading` forever despite being
// present (the live bug this fixes).
func TestLibraryScan_ConfirmsTMDBSeriesWhenItemHasBothIDs(t *testing.T) {
	ls, st, ms, _ := setupScan(t)
	key := provision.Key("series:tmdb:82728") // Bluey, TMDB-keyed (no TVDB id on the record)
	if err := st.UpsertTitle(context.Background(), provision.Record{
		Key: key, State: provision.Requested, Deadline: now.Add(12 * time.Hour),
		Title: provision.Title{MediaType: provision.Series, TMDBID: 82728},
	}); err != nil {
		t.Fatal(err)
	}
	// The library item carries BOTH ids (as Emby does) — Title.Key() would prefer tvdb:353546.
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-bluey", Name: "Bluey", Type: "Series", TMDBID: 82728, TVDBID: 353546},
	}

	n, err := ls.Incremental(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("confirmed %d, want 1 (TMDB-keyed series must match an item that has both ids)", n)
	}
	if got := getState(t, st, key); got != provision.Available {
		t.Errorf("state = %s, want available", got)
	}
}

// A library item that matches NO in-flight title is ignored — the scan never confirms a title
// that isn't awaiting the library.
func TestLibraryScan_IgnoresUntrackedItems(t *testing.T) {
	ls, st, ms, emit := setupScan(t)
	// One requested title (603) awaiting; the library reports a DIFFERENT movie (550).
	key := provision.Key("movie:tmdb:603")
	put(t, st, key, provision.Requested, 603, now.Add(24*time.Hour))
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-9", Name: "Fight Club", Type: "Movie", TMDBID: 550},
	}

	n, err := ls.Incremental(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("confirmed %d, want 0", n)
	}
	if got := getState(t, st, key); got != provision.Requested {
		t.Errorf("state = %s, want requested (unchanged)", got)
	}
	if len(emit.events) != 0 {
		t.Errorf("events = %+v, want none", emit.events)
	}
}

// A title already available (terminal) is not re-confirmed even if present in the scan — it's
// not in the in-flight set, so the scan never touches it (provision invariant 1).
func TestLibraryScan_SkipsTerminalTitles(t *testing.T) {
	ls, st, ms, emit := setupScan(t)
	key := provision.Key("movie:tmdb:603")
	put(t, st, key, provision.Available, 603, time.Time{})
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-1", Name: "The Matrix", Type: "Movie", TMDBID: 603},
	}

	n, err := ls.Incremental(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("confirmed %d, want 0", n)
	}
	if len(emit.events) != 0 {
		t.Errorf("events = %+v, want none", emit.events)
	}
}

// The full sweep confirms via AllItems the same way the incremental path does.
func TestLibraryScan_FullConfirms(t *testing.T) {
	ls, st, ms, _ := setupScan(t)
	key := provision.Key("movie:tmdb:603")
	put(t, st, key, provision.Requested, 603, now.Add(24*time.Hour))
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-1", Name: "The Matrix", Type: "Movie", TMDBID: 603},
	}

	n, err := ls.Full(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("confirmed %d, want 1", n)
	}
	if got := getState(t, st, key); got != provision.Available {
		t.Errorf("state = %s, want available", got)
	}
}

// With nothing in-flight, the scan short-circuits and confirms nothing even if the library has
// items — the common steady-state case, and it avoids a needless correlation pass.
func TestLibraryScan_NoInflightNoop(t *testing.T) {
	ls, _, ms, emit := setupScan(t)
	ms.SearchItems = []testkit.SearchStub{
		{LibraryItemID: "lib-1", Name: "The Matrix", Type: "Movie", TMDBID: 603},
	}
	n, err := ls.Incremental(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(emit.events) != 0 {
		t.Errorf("confirmed %d / %d events, want 0/0", n, len(emit.events))
	}
	if ms.LastEmbyToken != "" {
		t.Error("incremental scan contacted the media server with no in-flight titles")
	}
}
