package channels_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/channels"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/programmer"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

// --- test harness ---

func newStore(t *testing.T) store.Store {
	t.Helper()
	return testkit.MigratedSQLiteStore(t)
}

// mapAvail is a mutable Availability tests drive to simulate content landing.
type mapAvail map[provision.Key]string

func (m mapAvail) Resolve(k provision.Key) (string, int64, bool) {
	id, ok := m[k]
	return id, 0, ok
}
func (m mapAvail) ResolveEpisodes(provision.Key) schedule.EpisodeResolution {
	return schedule.EpisodeResolution{}
}

// seedChannel writes a channel with the given approved lineup, unreconciled.
func seedChannel(t *testing.T, st store.Store, id string, number int, entries ...schedule.LineupEntry) {
	t.Helper()
	ch := store.Channel{Lineup: entries}
	ch.ID = id
	ch.Name = "Ch " + id
	ch.Number = number
	ch.Strategy = schedule.Sequential
	ch.Status = schedule.StatusBuilding
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
}

func entry(key, title string) schedule.LineupEntry {
	return schedule.LineupEntry{Key: provision.Key(key), Title: title, DurationMs: 3600000}
}

func newEngine(st store.Store, tun programmer.Programmer, avail channels.Availability, guide channels.GuidePoker) *channels.Engine {
	return channels.New(st, tun, avail, guide, channels.Config{ReconcileTTL: 10 * time.Minute},
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, testkit.Logger())
}

func newEngineForBackend(
	st store.Store, tun programmer.Programmer, avail channels.Availability, guide channels.GuidePoker,
	backend func() string,
) *channels.Engine {
	return channels.New(st, tun, avail, guide, channels.Config{
		ReconcileTTL:                 10 * time.Minute,
		ResolvePlayoutBackendContext: func(context.Context) (string, error) { return backend(), nil },
	}, func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, testkit.Logger())
}

type recordedChannelChange struct {
	previousStatus string
	status         string
}

type recordingChannelNotifier struct {
	changes []recordedChannelChange
}

func (n *recordingChannelNotifier) ChannelChanged(_ string, previousStatus, status string) {
	n.changes = append(n.changes, recordedChannelChange{previousStatus: previousStatus, status: status})
}

// --- §19 gate tests ---

// A fresh channel with all content available reconciles to a live Tunarr channel;
// a second reconcile is a no-op (idempotent, minimal-diff).
func TestReconcile_CreatesThenIdempotent(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1", "movie:tmdb:2": "lib-2"}
	notifier := &recordingChannelNotifier{}
	e := newEngine(st, tun, avail, nil).WithNotifier(notifier)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"), entry("movie:tmdb:2", "B"))

	// First reconcile: creates the Tunarr channel + pushes the lineup.
	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if tun.Creates != 1 {
		t.Fatalf("want 1 create, got %d", tun.Creates)
	}
	if tun.Pushes != 1 {
		t.Fatalf("want 1 lineup push, got %d", tun.Pushes)
	}
	// The server-assigned id was persisted (Phase-0 finding 1).
	ch, _ := st.GetChannel(context.Background(), "c1")
	if ch.TunarrID == "" {
		t.Fatal("server-assigned TunarrID not persisted")
	}
	if ch.Status != schedule.StatusLive {
		t.Fatalf("status = %s, want live", ch.Status)
	}

	// Second reconcile with no input change: no new create, NO new push.
	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if tun.Creates != 1 {
		t.Errorf("idempotent reconcile created again: %d creates", tun.Creates)
	}
	if tun.Pushes != 1 {
		t.Errorf("idempotent reconcile re-pushed lineup: %d pushes (want 1)", tun.Pushes)
	}
	wantChanges := []recordedChannelChange{
		{previousStatus: string(schedule.StatusBuilding), status: string(schedule.StatusLive)},
		{previousStatus: string(schedule.StatusLive), status: string(schedule.StatusLive)},
	}
	if !slices.Equal(notifier.changes, wantChanges) {
		t.Fatalf("channel change signals = %+v, want %+v", notifier.changes, wantChanges)
	}
}

// Internal playout still needs reconciliation: Desired, status and the next durable sweep
// deadline are local control-plane state even though the encoder computes CyclePreview live.
// A nil Programmer is intentional — any accidental Tunarr call panics and fails this test.
func TestReconcile_InternalMaterializesWithoutProgrammer(t *testing.T) {
	st := newStore(t)
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	e := newEngineForBackend(st, nil, avail, nil,
		func() string { return schedule.PlayoutBackendInternal })
	seedChannel(t, st, "internal", 5, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "internal"); err != nil {
		t.Fatal(err)
	}
	ch, err := st.GetChannel(context.Background(), "internal")
	if err != nil {
		t.Fatal(err)
	}
	if ch.TunarrID != "" {
		t.Fatalf("internal reconcile fabricated a Tunarr id: %q", ch.TunarrID)
	}
	if ch.Status != schedule.StatusLive || programCount(ch) != 1 || ch.Desired[0].LibraryItemID != "lib-1" {
		t.Fatalf("internal desired state was not materialized: status=%s desired=%+v", ch.Status, ch.Desired)
	}
	wantAnchor := time.Unix(1_800_000_000, 0).UTC()
	if !ch.PlayoutAnchor.Equal(wantAnchor) {
		t.Fatalf("first-live playout anchor = %v, want %v", ch.PlayoutAnchor, wantAnchor)
	}
	wantDeadline := time.Unix(1_800_000_000, 0).UTC().Add(10 * time.Minute)
	if !ch.ReconcileDeadline.Equal(wantDeadline) {
		t.Fatalf("deadline = %v, want %v", ch.ReconcileDeadline, wantDeadline)
	}
}

func TestReconcilePublishesOutcomeToGenerationMetrics(t *testing.T) {
	st := newStore(t)
	recorder := metrics.New(metrics.Options{})
	engine := newEngineForBackend(st, nil, mapAvail{"movie:tmdb:1": "lib-1"}, nil,
		func() string { return schedule.PlayoutBackendInternal }).WithMetrics(recorder)
	seedChannel(t, st, "private-channel-id", 5, entry("movie:tmdb:1", "A"))

	if err := engine.Reconcile(t.Context(), "private-channel-id"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		`loomarr_channel_reconciles_total{result="success"} 1`,
		`loomarr_channel_reconcile_duration_seconds_count 1`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("generation scrape does not contain %q", want)
		}
	}
	if strings.Contains(response.Body.String(), "private-channel-id") {
		t.Fatal("generation scrape leaked a Channel id")
	}
}

func TestReconcileRecordsOnlyFirstLiveProposalSchedulingJourney(t *testing.T) {
	st := newStore(t)
	jobID := "private-proposal-job"
	if err := st.CreateJob(t.Context(), store.Job{
		ID: jobID, Kind: "suggest", Status: "done", IntentJSON: `{}`, IntentHash: "intent",
		CreatedAt: time.Unix(1_799_999_000, 0).UTC(), UpdatedAt: time.Unix(1_799_999_000, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	seedChannel(t, st, "quality-channel", 5, entry("movie:tmdb:1", "A"))
	ch, err := st.GetChannel(t.Context(), "quality-channel")
	if err != nil {
		t.Fatal(err)
	}
	ch.IntentRef = jobID
	if _, err := st.SaveChannel(t.Context(), ch); err != nil {
		t.Fatal(err)
	}

	sink := &testkit.QualityRecorder{}
	engine := newEngineForBackend(st, nil, mapAvail{"movie:tmdb:1": "lib-1"}, nil,
		func() string { return schedule.PlayoutBackendInternal }).
		WithQualityRecorder(quality.NewSchedulingRecorder(sink, testkit.Logger()))
	if err := engine.Reconcile(t.Context(), "quality-channel"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reconcile(t.Context(), "quality-channel"); err != nil {
		t.Fatal(err)
	}

	got := sink.Observations()
	if len(got) != 1 || got[0].Stage != quality.StageScheduling || got[0].Outcome != quality.OutcomeScheduled {
		t.Fatalf("scheduling observations = %+v", got)
	}
	job, err := st.GetJob(t.Context(), jobID)
	if err != nil || !job.ReachedLive {
		t.Fatalf("first-live milestone = %+v, %v", job, err)
	}
}

func TestReconcileRecordsFailureThenRecoveryBeforeFirstLive(t *testing.T) {
	st := newStore(t)
	jobID := "recovering-proposal-job"
	if err := st.CreateJob(t.Context(), store.Job{
		ID: jobID, Kind: "suggest", Status: "done", IntentJSON: `{}`, IntentHash: "intent",
		CreatedAt: time.Unix(1_799_999_000, 0).UTC(), UpdatedAt: time.Unix(1_799_999_000, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	seedChannel(t, st, "recovering-channel", 5, entry("movie:tmdb:1", "A"))
	ch, err := st.GetChannel(t.Context(), "recovering-channel")
	if err != nil {
		t.Fatal(err)
	}
	ch.IntentRef = jobID
	if _, err := st.SaveChannel(t.Context(), ch); err != nil {
		t.Fatal(err)
	}

	fail := true
	sink := &testkit.QualityRecorder{}
	engine := channels.New(st, nil, mapAvail{"movie:tmdb:1": "lib-1"}, nil, channels.Config{
		ReconcileTTL: 10 * time.Minute,
		ResolvePlayoutBackendContext: func(context.Context) (string, error) {
			if fail {
				return "", errors.New("backend unavailable")
			}
			return schedule.PlayoutBackendInternal, nil
		},
	}, func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, testkit.Logger()).
		WithQualityRecorder(quality.NewSchedulingRecorder(sink, testkit.Logger()))

	if err := engine.Reconcile(t.Context(), "recovering-channel"); err == nil {
		t.Fatal("first reconcile unexpectedly succeeded")
	}
	fail = false
	if err := engine.Reconcile(t.Context(), "recovering-channel"); err != nil {
		t.Fatal(err)
	}

	got := sink.Observations()
	if len(got) != 2 || got[0].Outcome != quality.OutcomeFailed || got[1].Outcome != quality.OutcomeScheduled {
		t.Fatalf("scheduling recovery observations = %+v", got)
	}
}

func TestReconcileDoesNotCallEmptyOrAlreadyLiveSchedulingQuality(t *testing.T) {
	st := newStore(t)
	jobID := "empty-proposal-job"
	if err := st.CreateJob(t.Context(), store.Job{
		ID: jobID, Kind: "suggest", Status: "done", IntentJSON: `{}`, IntentHash: "intent",
		CreatedAt: time.Unix(1_799_999_000, 0).UTC(), UpdatedAt: time.Unix(1_799_999_000, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	seedChannel(t, st, "empty-quality-channel", 5, entry("movie:tmdb:1", "A"))
	ch, err := st.GetChannel(t.Context(), "empty-quality-channel")
	if err != nil {
		t.Fatal(err)
	}
	ch.IntentRef = jobID
	if _, err := st.SaveChannel(t.Context(), ch); err != nil {
		t.Fatal(err)
	}
	sink := &testkit.QualityRecorder{}
	engine := newEngineForBackend(st, nil, mapAvail{}, nil,
		func() string { return schedule.PlayoutBackendInternal }).
		WithQualityRecorder(quality.NewSchedulingRecorder(sink, testkit.Logger()))

	if err := engine.Reconcile(t.Context(), "empty-quality-channel"); err != nil {
		t.Fatal(err)
	}
	if got := sink.Observations(); len(got) != 0 {
		t.Fatalf("empty reconcile recorded scheduling quality: %+v", got)
	}
	job, err := st.GetJob(t.Context(), jobID)
	if err != nil || job.ReachedLive {
		t.Fatalf("empty reconcile changed first-live milestone: %+v, %v", job, err)
	}
}

func TestReconcile_PreservesFirstLivePlayoutAnchor(t *testing.T) {
	st := newStore(t)
	clock := time.Unix(1_800_000_000, 0).UTC()
	e := channels.New(st, nil, mapAvail{"movie:tmdb:1": "lib-1"}, nil, channels.Config{
		ReconcileTTL:                 10 * time.Minute,
		ResolvePlayoutBackendContext: func(context.Context) (string, error) { return schedule.PlayoutBackendInternal, nil },
	}, func() time.Time { return clock }, testkit.Logger())
	seedChannel(t, st, "anchored", 15, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "anchored"); err != nil {
		t.Fatal(err)
	}
	first, _ := st.GetChannel(context.Background(), "anchored")
	clock = clock.Add(2 * time.Hour)
	if err := e.Reconcile(context.Background(), "anchored"); err != nil {
		t.Fatal(err)
	}
	again, _ := st.GetChannel(context.Background(), "anchored")
	if !again.PlayoutAnchor.Equal(first.PlayoutAnchor) {
		t.Fatalf("reconcile moved anchor from %v to %v", first.PlayoutAnchor, again.PlayoutAnchor)
	}
}

func TestReconcile_InternalFirstMaterializationRescansTunerOnce(t *testing.T) {
	st := newStore(t)
	guide := &fakeGuide{}
	e := newEngineForBackend(st, nil, mapAvail{"movie:tmdb:1": "lib-1"}, guide,
		func() string { return schedule.PlayoutBackendInternal })
	seedChannel(t, st, "internal-guide", 6, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "internal-guide"); err != nil {
		t.Fatal(err)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("first internal materialization freshness = %d rescans/%d pokes, want 1/0",
			guide.rescans, guide.pokes)
	}

	if err := e.Reconcile(context.Background(), "internal-guide"); err != nil {
		t.Fatal(err)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("unchanged internal reconcile touched freshness: %d rescans/%d pokes, want 1/0",
			guide.rescans, guide.pokes)
	}
}

func TestReconcile_InternalDesiredChangeRefreshesGuide(t *testing.T) {
	st := newStore(t)
	guide := &fakeGuide{}
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	e := newEngineForBackend(st, nil, avail, guide,
		func() string { return schedule.PlayoutBackendInternal })
	playout := &testkit.Playout{}
	e.WithScheduleInvalidator(playout)
	seedChannel(t, st, "internal-change", 7,
		entry("movie:tmdb:1", "A"), entry("movie:tmdb:2", "B"))

	if err := e.Reconcile(context.Background(), "internal-change"); err != nil {
		t.Fatal(err)
	}
	guide.pokes, guide.rescans = 0, 0
	stopsAfterMaterialization := len(playout.StoppedChannels())

	avail["movie:tmdb:2"] = "lib-2"
	if err := e.Reconcile(context.Background(), "internal-change"); err != nil {
		t.Fatal(err)
	}
	if guide.rescans != 0 || guide.pokes != 1 {
		t.Fatalf("internal desired change freshness = %d rescans/%d pokes, want 0/1",
			guide.rescans, guide.pokes)
	}
	if got := len(playout.StoppedChannels()); got != stopsAfterMaterialization+1 {
		t.Fatalf("changed internal cycle produced %d total session stops, want %d", got, stopsAfterMaterialization+1)
	}

	if err := e.Reconcile(context.Background(), "internal-change"); err != nil {
		t.Fatal(err)
	}
	if got := len(playout.StoppedChannels()); got != stopsAfterMaterialization+1 {
		t.Fatalf("unchanged reconcile restarted internal playout: %d stops", got)
	}
}

// An empty internal channel is absent from the surfable M3U catalog. When its first title
// lands, the availability event must re-scan the tuner so the channel appears; a guide-only
// refresh cannot add it. Repeated empty or settled-live reconciles remain quiet.
func TestReconcile_InternalEmptyBecomesPlayableRescansTuner(t *testing.T) {
	st := newStore(t)
	guide := &fakeGuide{}
	avail := mapAvail{}
	e := newEngineForBackend(st, nil, avail, guide,
		func() string { return schedule.PlayoutBackendInternal })
	seedChannel(t, st, "internal-empty", 8, entry("movie:tmdb:1", "A"))

	ch, err := st.GetChannel(context.Background(), "internal-empty")
	if err != nil {
		t.Fatal(err)
	}
	ch.Status = schedule.StatusEmpty
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}

	// Materialize the pending local desired shape, then clear that first-write effect so the
	// next pass is a genuinely unchanged Empty reconcile.
	if err := e.Reconcile(context.Background(), "internal-empty"); err != nil {
		t.Fatal(err)
	}
	guide.rescans, guide.pokes = 0, 0

	// Still empty and unchanged: no catalog or guide freshness work is warranted.
	if err := e.Reconcile(context.Background(), "internal-empty"); err != nil {
		t.Fatal(err)
	}
	if guide.rescans != 0 || guide.pokes != 0 {
		t.Fatalf("unchanged empty reconcile touched freshness: %d rescans/%d pokes",
			guide.rescans, guide.pokes)
	}

	// The title lands without a channel PATCH, so the row is still Empty rather than Building.
	avail["movie:tmdb:1"] = "lib-1"
	e.OnAvailability(context.Background(), provision.DomainEvent{
		Key: "movie:tmdb:1", State: provision.Available,
	})

	ch, err = st.GetChannel(context.Background(), "internal-empty")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Status != schedule.StatusLive || programCount(ch) != 1 {
		t.Fatalf("availability event did not make internal channel playable: status=%s desired=%+v",
			ch.Status, ch.Desired)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("empty to playable freshness = %d rescans/%d pokes, want 1/0",
			guide.rescans, guide.pokes)
	}

	if err := e.Reconcile(context.Background(), "internal-empty"); err != nil {
		t.Fatal(err)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("unchanged live reconcile touched freshness: %d rescans/%d pokes",
			guide.rescans, guide.pokes)
	}
}

// Empty internal channels leave the tuner catalog. A policy edit can filter an otherwise
// available live lineup to nothing without changing the channel identity, so the reconciler
// itself must recognize live -> empty as M3U membership removal rather than an EPG-only edit.
func TestReconcile_InternalLiveBecomesEmptyRescansTuner(t *testing.T) {
	st := newStore(t)
	guide := &fakeGuide{}
	e := newEngineForBackend(st, nil, mapAvail{"movie:tmdb:1": "lib-1"}, guide,
		func() string { return schedule.PlayoutBackendInternal })
	seedChannel(t, st, "internal-live", 9, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "internal-live"); err != nil {
		t.Fatal(err)
	}
	guide.rescans, guide.pokes = 0, 0

	ch, err := st.GetChannel(context.Background(), "internal-live")
	if err != nil {
		t.Fatal(err)
	}
	ch.Policy.Seasonal = schedule.SeasonalPolicy{
		Mode: schedule.SeasonalExclusive, Holidays: []string{"halloween"}, OffSeason: schedule.OffSeasonDark,
	}
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconcile(context.Background(), "internal-live"); err != nil {
		t.Fatal(err)
	}

	ch, err = st.GetChannel(context.Background(), "internal-live")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Status != schedule.StatusEmpty || programCount(ch) != 0 {
		t.Fatalf("filtered internal channel = status %s desired %+v, want empty", ch.Status, ch.Desired)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("live to empty freshness = %d rescans/%d pokes, want 1/0",
			guide.rescans, guide.pokes)
	}
}

func TestReconcile_PlayoutBackendPrecedence(t *testing.T) {
	tests := []struct {
		name          string
		global        string
		override      string
		wantProjected bool
	}{
		{name: "channel internal overrides global Tunarr", global: "tunarr", override: schedule.PlayoutBackendInternal},
		{name: "channel Tunarr overrides global internal", global: schedule.PlayoutBackendInternal, override: "tunarr", wantProjected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newStore(t)
			tun := testkit.NewTunarr()
			e := newEngineForBackend(st, tun, mapAvail{"movie:tmdb:1": "lib-1"}, nil,
				func() string { return tt.global })
			seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"))
			ch, err := st.GetChannel(context.Background(), "c1")
			if err != nil {
				t.Fatal(err)
			}
			ch.Policy.Playout = &schedule.PlayoutPolicy{Backend: tt.override}
			if _, err := st.SaveChannel(context.Background(), ch); err != nil {
				t.Fatal(err)
			}

			if err := e.Reconcile(context.Background(), "c1"); err != nil {
				t.Fatal(err)
			}
			if got := tun.Creates > 0; got != tt.wantProjected {
				t.Fatalf("Tunarr projected = %v, want %v (creates=%d)", got, tt.wantProjected, tun.Creates)
			}
		})
	}
}

// Moving to internal playout is reversible, not an implicit purge: retain the historical
// remote identity and leave Tunarr untouched. Moving back reuses that identity.
func TestReconcile_BackendSwitchPreservesTunarrProjection(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	guide := &fakeGuide{}
	backend := "tunarr"
	e := newEngineForBackend(st, tun, mapAvail{"movie:tmdb:1": "lib-1"}, guide,
		func() string { return backend })
	seedChannel(t, st, "switch", 5, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "switch"); err != nil {
		t.Fatal(err)
	}
	projected, err := st.GetChannel(context.Background(), "switch")
	if err != nil {
		t.Fatal(err)
	}
	if projected.TunarrID == "" {
		t.Fatal("initial Tunarr projection did not persist its identity")
	}
	creates, updates, pushes, deletes := tun.Creates, tun.Updates, tun.Pushes, tun.Deletes

	backend = schedule.PlayoutBackendInternal
	projected.Status = schedule.StatusBuilding
	if _, err := st.SaveChannel(context.Background(), projected); err != nil {
		t.Fatal(err)
	}
	guide.pokes, guide.rescans = 0, 0
	if err := e.Reconcile(context.Background(), "switch"); err != nil {
		t.Fatal(err)
	}
	internal, err := st.GetChannel(context.Background(), "switch")
	if err != nil {
		t.Fatal(err)
	}
	if internal.TunarrID != projected.TunarrID {
		t.Fatalf("backend switch changed historical Tunarr id: %q → %q", projected.TunarrID, internal.TunarrID)
	}
	if tun.Creates != creates || tun.Updates != updates || tun.Pushes != pushes || tun.Deletes != deletes {
		t.Fatalf("internal reconcile touched Tunarr: creates=%d→%d updates=%d→%d pushes=%d→%d deletes=%d→%d",
			creates, tun.Creates, updates, tun.Updates, pushes, tun.Pushes, deletes, tun.Deletes)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("switch to internal freshness = %d rescans/%d pokes, want 1/0",
			guide.rescans, guide.pokes)
	}

	backend = "tunarr"
	internal.Status = schedule.StatusBuilding
	if _, err := st.SaveChannel(context.Background(), internal); err != nil {
		t.Fatal(err)
	}
	guide.pokes, guide.rescans = 0, 0
	if err := e.Reconcile(context.Background(), "switch"); err != nil {
		t.Fatal(err)
	}
	if tun.Creates != creates {
		t.Fatalf("switching back recreated instead of reusing %q: creates=%d→%d", projected.TunarrID, creates, tun.Creates)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("switch back to Tunarr freshness = %d rescans/%d pokes, want 1/0",
			guide.rescans, guide.pokes)
	}
}

func TestReconcile_ReadsLiveCooldownSetting(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	now := time.Unix(1_800_000_000, 0).UTC()
	cooldown := 10 * time.Minute
	e := channels.New(st, tun, avail, nil, channels.Config{
		ResolveReconcileTTL: func() time.Duration { return cooldown },
	}, func() time.Time { return now }, testkit.Logger())
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	ch, _ := st.GetChannel(context.Background(), "c1")
	if want := now.Add(10 * time.Minute); !ch.ReconcileDeadline.Equal(want) {
		t.Fatalf("initial deadline = %v, want %v", ch.ReconcileDeadline, want)
	}

	cooldown = 30 * time.Minute
	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	ch, _ = st.GetChannel(context.Background(), "c1")
	if want := now.Add(30 * time.Minute); !ch.ReconcileDeadline.Equal(want) {
		t.Fatalf("deadline after settings change = %v, want %v", ch.ReconcileDeadline, want)
	}
}

// A lineup-push failure right after channel creation must NOT lose the new Tunarr
// id: the create is checkpointed to the store before the push. So the next reconcile
// UPDATES the existing channel instead of re-creating it — the fix for the live-smoke
// orphan loop, where a push timeout discarded the id and every retry re-created the
// channel at the same number and collided (a 500). Regression guard for §9 atomicity.
func TestReconcile_ChannelIDCheckpointedBeforeLineupPush(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	tun.SetLineupErr = context.DeadlineExceeded // simulate the big-library resolve timeout
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	e := newEngine(st, tun, avail, nil)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"))

	// First reconcile: the channel IS created in Tunarr, but the lineup push fails →
	// the reconcile returns an error.
	if err := e.Reconcile(context.Background(), "c1"); err == nil {
		t.Fatal("expected the reconcile to surface the lineup-push failure")
	}
	if tun.Creates != 1 {
		t.Fatalf("channel should have been created once, got %d creates", tun.Creates)
	}
	// The critical assertion: despite the push failure, the new id was checkpointed.
	ch, _ := st.GetChannel(context.Background(), "c1")
	if ch.TunarrID == "" {
		t.Fatal("new Tunarr id was NOT checkpointed — a retry will re-create and collide")
	}
	checkpointed := ch.TunarrID

	// Clear the injected failure and reconcile again: it must UPDATE the same channel,
	// never create a second one (no orphan, no number collision).
	tun.SetLineupErr = nil
	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatalf("retry reconcile should succeed once the push works, got %v", err)
	}
	if tun.Creates != 1 {
		t.Errorf("retry RE-CREATED the channel (%d creates) — the orphan-loop bug", tun.Creates)
	}
	if tun.Pushes != 1 {
		t.Errorf("retry should have pushed the lineup once, got %d", tun.Pushes)
	}
	ch, _ = st.GetChannel(context.Background(), "c1")
	if ch.TunarrID != checkpointed {
		t.Errorf("id changed across retry: %q → %q (should be stable)", checkpointed, ch.TunarrID)
	}
	if ch.Status != schedule.StatusLive {
		t.Errorf("after a successful retry, status = %s, want live", ch.Status)
	}
}

func TestReconcile_CheckpointPersistsAutoRenumberBeforeLineupFailure(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	tun.SeedForeignChannel(5, "Do not touch")
	tun.SetLineupErr = context.DeadlineExceeded
	e := newEngine(st, tun, mapAvail{"movie:tmdb:1": "lib-1"}, nil)
	seedChannel(t, st, "c-renumber-checkpoint", 5, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "c-renumber-checkpoint"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first reconcile = %v, want lineup failure", err)
	}
	checkpointed, err := st.GetChannel(context.Background(), "c-renumber-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if checkpointed.TunarrID == "" || checkpointed.Number == 5 {
		t.Fatalf("checkpoint lost remote identity/renumber: id=%q number=%d", checkpointed.TunarrID, checkpointed.Number)
	}

	tun.SetLineupErr = nil
	if err := e.Reconcile(context.Background(), "c-renumber-checkpoint"); err != nil {
		t.Fatal(err)
	}
	if tun.Creates != 1 {
		t.Fatalf("retry recreated auto-renumbered channel: creates=%d", tun.Creates)
	}
	got, err := st.GetChannel(context.Background(), "c-renumber-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if got.TunarrID != checkpointed.TunarrID || got.Number != checkpointed.Number {
		t.Fatalf("retry drifted checkpoint: before=%q/%d after=%q/%d",
			checkpointed.TunarrID, checkpointed.Number, got.TunarrID, got.Number)
	}
}

// A channel edit can land after reconcile has read its snapshot and even after a stale
// lineup has reached Tunarr. The final channel save is a CAS: losing it must restart the
// whole plan from the new row, preserving the operator edit and converging Tunarr to it.
func TestReconcile_ReloadsAfterConcurrentChannelEdit(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1", "movie:tmdb:2": "lib-2"}
	e := newEngine(st, tun, avail, nil)
	seedChannel(t, st, "c-race", 15, entry("movie:tmdb:1", "Old"))

	setLineupStarted := make(chan struct{})
	allowSetLineup := make(chan struct{})
	var once sync.Once
	tun.BeforeSetLineup = func(_ string, _ []schedule.Slot) {
		once.Do(func() {
			close(setLineupStarted)
			<-allowSetLineup
		})
	}

	done := make(chan error, 1)
	go func() { done <- e.Reconcile(context.Background(), "c-race") }()
	<-setLineupStarted

	// Commit a real competing writer while reconcile is holding the older snapshot.
	// SaveChannel advances the revision, so the first reconcile attempt cannot replace it.
	edited, err := st.GetChannel(context.Background(), "c-race")
	if err != nil {
		t.Fatal(err)
	}
	edited.Name = "Operator edit"
	edited.Lineup = []schedule.LineupEntry{entry("movie:tmdb:2", "New")}
	if _, err := st.SaveChannel(context.Background(), edited); err != nil {
		t.Fatal(err)
	}
	close(allowSetLineup)

	if err := <-done; err != nil {
		t.Fatalf("reconcile did not converge after a stale save: %v", err)
	}
	got, err := st.GetChannel(context.Background(), "c-race")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Operator edit" || len(got.Lineup) != 1 || got.Lineup[0].Key != "movie:tmdb:2" {
		t.Fatalf("concurrent operator edit was lost: name=%q lineup=%+v", got.Name, got.Lineup)
	}
	if len(got.Desired) != 1 || got.Desired[0].LibraryItemID != "lib-2" {
		t.Fatalf("desired lineup was not recomputed from the winning edit: %+v", got.Desired)
	}
	actual, err := tun.GetLineup(context.Background(), got.TunarrID)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 1 || actual[0].LibraryItemID != "lib-2" {
		t.Fatalf("Tunarr retained the stale push instead of the retry's lineup: %+v", actual)
	}
}

// reconcileRun carries remote effects across CAS retries so a Tunarr create is eventually
// followed by the right media-server poke. Those flags must not leak when the winning edit
// switches the channel to internal playout: the retry persists local truth and stops projecting.
func TestReconcile_BackendSwitchDuringRetryDoesNotLeakTunarrEffects(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	guide := &fakeGuide{}
	avail := mapAvail{
		"movie:tmdb:1": "lib-1",
		"movie:tmdb:2": "lib-2",
	}
	e := newEngineForBackend(st, tun, avail, guide, func() string { return "tunarr" })
	seedChannel(t, st, "backend-race", 15, entry("movie:tmdb:1", "Initial"))
	if err := e.Reconcile(context.Background(), "backend-race"); err != nil {
		t.Fatal(err)
	}
	guide.pokes, guide.rescans = 0, 0

	// Make the next Tunarr projection differ, then pause it after the stale lineup is planned.
	ch, err := st.GetChannel(context.Background(), "backend-race")
	if err != nil {
		t.Fatal(err)
	}
	ch.Lineup = []schedule.LineupEntry{entry("movie:tmdb:2", "Stale projection")}
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}

	setLineupStarted := make(chan struct{})
	allowSetLineup := make(chan struct{})
	var once sync.Once
	tun.BeforeSetLineup = func(_ string, _ []schedule.Slot) {
		once.Do(func() {
			close(setLineupStarted)
			<-allowSetLineup
		})
	}

	done := make(chan error, 1)
	go func() { done <- e.Reconcile(context.Background(), "backend-race") }()
	<-setLineupStarted

	// This writer wins the local row while the old Tunarr attempt is in flight.
	latest, err := st.GetChannel(context.Background(), "backend-race")
	if err != nil {
		t.Fatal(err)
	}
	latest.Policy.Playout = &schedule.PlayoutPolicy{Backend: schedule.PlayoutBackendInternal}
	// Return to the last COMMITTED desired lineup. The winning internal retry therefore
	// has no local guide change; any poke can only have leaked from the stale Tunarr push.
	latest.Lineup = []schedule.LineupEntry{entry("movie:tmdb:1", "Initial")}
	if _, err := st.SaveChannel(context.Background(), latest); err != nil {
		t.Fatal(err)
	}
	close(allowSetLineup)

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := st.GetChannel(context.Background(), "backend-race")
	if err != nil {
		t.Fatal(err)
	}
	if !schedule.PlaysInternally(got.Policy, "tunarr") || len(got.Desired) != 1 || got.Desired[0].LibraryItemID != "lib-1" {
		t.Fatalf("internal retry did not persist the winning desired state: %+v", got)
	}
	if guide.pokes != 0 || guide.rescans != 0 {
		t.Fatalf("stale Tunarr effects leaked through internal retry: pokes=%d rescans=%d", guide.pokes, guide.rescans)
	}
}

// The new remote id is checkpointed with a targeted write. If another channel writer
// advanced the revision during the remote create, AttachTunarrChannel still makes that id
// durable, then reconcile restarts before pushing the stale lineup. That gives both sides:
// no orphan shell and no operator edit overwritten by the checkpoint.
func TestReconcile_CheckpointPreservesAnEditDuringRemoteCreate(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1", "movie:tmdb:2": "lib-2"}
	e := newEngine(st, tun, avail, nil)
	seedChannel(t, st, "c-checkpoint-race", 16, entry("movie:tmdb:1", "Old"))

	ensureStarted := make(chan struct{})
	allowEnsure := make(chan struct{})
	var once sync.Once
	tun.BeforeEnsureChannel = func(_ programmer.ChannelSpec) {
		once.Do(func() {
			close(ensureStarted)
			<-allowEnsure
		})
	}

	done := make(chan error, 1)
	go func() { done <- e.Reconcile(context.Background(), "c-checkpoint-race") }()
	<-ensureStarted

	edited, err := st.GetChannel(context.Background(), "c-checkpoint-race")
	if err != nil {
		t.Fatal(err)
	}
	edited.Name = "Edited during create"
	edited.Lineup = []schedule.LineupEntry{entry("movie:tmdb:2", "New")}
	if _, err := st.SaveChannel(context.Background(), edited); err != nil {
		t.Fatal(err)
	}
	close(allowEnsure)

	if err := <-done; err != nil {
		t.Fatalf("reconcile did not restart after the checkpoint observed a newer row: %v", err)
	}
	got, err := st.GetChannel(context.Background(), "c-checkpoint-race")
	if err != nil {
		t.Fatal(err)
	}
	if tun.Creates != 1 {
		t.Fatalf("stale checkpoint recreated the remote channel: creates=%d", tun.Creates)
	}
	if tun.Pushes != 1 {
		t.Fatalf("stale plan reached Tunarr before restart: pushes=%d, want only the fresh push", tun.Pushes)
	}
	if got.TunarrID == "" || got.Name != "Edited during create" || got.Lineup[0].Key != "movie:tmdb:2" {
		t.Fatalf("checkpoint did not preserve both identity and edit: %+v", got)
	}
	actual, ok, err := tun.GetChannel(context.Background(), got.TunarrID)
	if err != nil || !ok {
		t.Fatalf("Tunarr channel missing after checkpoint retry: ok=%v err=%v", ok, err)
	}
	if actual.Name != "Edited during create" || actual.ProgramCount != 1 {
		t.Fatalf("Tunarr did not converge from the reloaded row: %+v", actual)
	}
}

func TestReconcile_RemovesRemoteCreateThatLosesCrossEngineAttachRace(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		name := "fresh"
		if recreate {
			name = "out-of-band recreation"
		}
		t.Run(name, func(t *testing.T) {
			st := newStore(t)
			tun := testkit.NewTunarr()
			avail := mapAvail{"movie:tmdb:1": "lib-1"}
			firstEngine := newEngine(st, tun, avail, nil)
			secondEngine := newEngine(st, tun, avail, nil)
			seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"))

			if recreate {
				if err := firstEngine.Reconcile(context.Background(), "c1"); err != nil {
					t.Fatal(err)
				}
				durable, err := st.GetChannel(context.Background(), "c1")
				if err != nil {
					t.Fatal(err)
				}
				if err := tun.DeleteChannel(context.Background(), durable.TunarrID); err != nil {
					t.Fatal(err)
				}
			}
			createsBefore, deletesBefore := tun.Creates, tun.Deletes

			firstCreating := make(chan struct{})
			releaseFirst := make(chan struct{})
			var once sync.Once
			tun.BeforeEnsureChannel = func(spec programmer.ChannelSpec) {
				if spec.TunarrID == "" && spec.Number == 5 {
					once.Do(func() { close(firstCreating) })
					<-releaseFirst
				}
			}

			firstErr := make(chan error, 1)
			go func() { firstErr <- firstEngine.Reconcile(context.Background(), "c1") }()
			<-firstCreating

			// A concurrent operator edit gives the second replica a different free number, so
			// both remote creates can succeed before the first replica attempts its attachment.
			ch, err := st.GetChannel(context.Background(), "c1")
			if err != nil {
				t.Fatal(err)
			}
			ch.Number = 6
			if _, err := st.SaveChannel(context.Background(), ch); err != nil {
				t.Fatal(err)
			}
			if err := secondEngine.Reconcile(context.Background(), "c1"); err != nil {
				t.Fatal(err)
			}
			close(releaseFirst)
			if err := <-firstErr; err != nil {
				t.Fatal(err)
			}

			durable, err := st.GetChannel(context.Background(), "c1")
			if err != nil {
				t.Fatal(err)
			}
			remote, err := tun.ListChannels(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tun.Creates-createsBefore != 2 || tun.Deletes-deletesBefore != 1 {
				t.Fatalf("race creates/deletes = %d/%d, want 2/1",
					tun.Creates-createsBefore, tun.Deletes-deletesBefore)
			}
			if len(remote) != 1 || remote[0].TunarrID != durable.TunarrID || remote[0].Number != 6 {
				t.Fatalf("remote channels = %+v, durable id/number = %q/%d",
					remote, durable.TunarrID, durable.Number)
			}
		})
	}
}

// Purge performs a remote delete before its local delete. A channel edit committed
// in that interval must make the revision-guarded local delete fail: the surviving
// row is the durable truth and a later reconcile can recreate its removed Tunarr peer.
func TestPurge_DoesNotDeleteAConcurrentlyEditedChannel(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	e := newEngine(st, tun, mapAvail{"movie:tmdb:1": "lib-1"}, nil)
	seedChannel(t, st, "c-purge-race", 17, entry("movie:tmdb:1", "A"))
	if err := e.Reconcile(context.Background(), "c-purge-race"); err != nil {
		t.Fatal(err)
	}

	deleteStarted := make(chan struct{})
	allowDelete := make(chan struct{})
	tun.BeforeDeleteChannel = func(_ string) {
		close(deleteStarted)
		<-allowDelete
	}

	done := make(chan error, 1)
	go func() { done <- e.Purge(context.Background(), "c-purge-race") }()
	<-deleteStarted

	edited, err := st.GetChannel(context.Background(), "c-purge-race")
	if err != nil {
		t.Fatal(err)
	}
	edited.Name = "Keep this edit"
	if _, err := st.SaveChannel(context.Background(), edited); err != nil {
		t.Fatal(err)
	}
	close(allowDelete)

	if err := <-done; !errors.Is(err, store.ErrChannelStale) {
		t.Fatalf("purge error = %v, want ErrChannelStale", err)
	}
	got, err := st.GetChannel(context.Background(), "c-purge-race")
	if err != nil {
		t.Fatalf("stale purge deleted the newer row: %v", err)
	}
	if got.Name != "Keep this edit" {
		t.Fatalf("surviving row lost the concurrent edit: %+v", got)
	}
}

func TestPurge_InternalHistoricalTunarrIDFailsClosedWithoutProgrammer(t *testing.T) {
	st := newStore(t)
	seedChannel(t, st, "internal-purge", 18, entry("movie:tmdb:1", "A"))
	ch, err := st.GetChannel(context.Background(), "internal-purge")
	if err != nil {
		t.Fatal(err)
	}
	ch.TunarrID = "historical-tunarr-id"
	ch.Policy.Playout = &schedule.PlayoutPolicy{Backend: schedule.PlayoutBackendInternal}
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	e := newEngineForBackend(st, nil, mapAvail{}, nil,
		func() string { return schedule.PlayoutBackendInternal })

	if err := e.Purge(context.Background(), "internal-purge"); err == nil {
		t.Fatal("purge succeeded without a Programmer despite a historical Tunarr projection")
	}
	if _, err := st.GetChannel(context.Background(), "internal-purge"); err != nil {
		t.Fatalf("fail-closed purge removed the only durable Tunarr identity: %v", err)
	}
}

func TestPurge_RescansTunerAfterHardDelete(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	guide := &fakeGuide{}
	e := newEngine(st, tun, mapAvail{"movie:tmdb:1": "lib-1"}, guide)
	seedChannel(t, st, "purge-rescan", 19, entry("movie:tmdb:1", "A"))
	if err := e.Reconcile(context.Background(), "purge-rescan"); err != nil {
		t.Fatal(err)
	}
	guide.pokes, guide.rescans = 0, 0

	if err := e.Purge(context.Background(), "purge-rescan"); err != nil {
		t.Fatal(err)
	}
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Fatalf("successful purge freshness = %d rescans/%d pokes, want 1/0",
			guide.rescans, guide.pokes)
	}
}

// fakeRatings is a RatingResolver over a fixed key→rating map (present = ok).
type fakeRatings map[provision.Key]string

func (f fakeRatings) Rating(_ context.Context, k provision.Key) (string, bool, error) {
	r, ok := f[k]
	return r, ok, nil
}

// §389 amendment: an entry that reached the scheduler UNRATED (an acquisition that
// wasn't in the library at proposal time, or a pre-fix cached proposal) is healed
// from the library at reconcile — otherwise a fail-closed audience ceiling drops it
// and the channel plays nothing (§9 dead air, the shape of FINDING 6). The heal is
// persisted, so it happens once.
func TestReconcile_HealsUnratedEntryFromLibrary(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	// The library rates it TV-Y7 — within the channel's kids ceiling.
	e := newEngine(st, tun, avail, nil).
		WithRatings(fakeRatings{"movie:tmdb:1": "TV-Y7"})

	// A channel whose approved entry has NO rating, under a TV-Y7 audience ceiling.
	ch := store.Channel{Lineup: []schedule.LineupEntry{
		{Key: "movie:tmdb:1", Title: "Kids Pick", DurationMs: 3600000}, // OfficialRating unset
	}}
	ch.ID, ch.Number, ch.Strategy, ch.Status = "cheal", 7, schedule.Sequential, schedule.StatusBuilding
	ch.Policy = schedule.ChannelPolicy{ProposalPolicy: schedule.ProposalPolicy{Audience: schedule.AudiencePolicy{Ceiling: "TV-Y7"}}}
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}

	if err := e.Reconcile(context.Background(), "cheal"); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetChannel(context.Background(), "cheal")
	// Healed AND persisted, so a future reconcile skips the lookup.
	if got.Lineup[0].OfficialRating != "TV-Y7" {
		t.Errorf("entry rating = %q, want TV-Y7 (healed from the library + persisted)", got.Lineup[0].OfficialRating)
	}
	// And the payoff: the entry survived the audience gate and became a program,
	// rather than being dropped to dead air.
	if n := programCount(got); n < 1 {
		t.Errorf("channel has %d programs — the unrated entry was dropped instead of healed", n)
	}
}

// countingBoxSets is a BoxSetResolver over a fixed key→collections map that COUNTS calls, so
// a test can prove the heal is one-time. A key absent from the map resolves to "belongs to no
// collection" (empty, ok=true) — a real settled answer, not a failure.
type countingBoxSets struct {
	sets  map[provision.Key][]string
	calls int
}

func (c *countingBoxSets) BoxSets(_ context.Context, k provision.Key) ([]string, bool, error) {
	c.calls++
	return c.sets[k], true, nil
}

// Media-server collection membership is stamped at reconcile and PERSISTED, so the scheduler
// filters on it with no library I/O (programming-design §2.2).
func TestReconcile_StampsBoxSetMembership(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	res := &countingBoxSets{sets: map[provision.Key][]string{"movie:tmdb:1": {"star-trek"}}}
	e := newEngine(st, tun, avail, nil).WithBoxSets(res)

	seedChannel(t, st, "cbs", 8, entry("movie:tmdb:1", "A"))
	if err := e.Reconcile(context.Background(), "cbs"); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetChannel(context.Background(), "cbs")
	if ids := got.Lineup[0].BoxSetIDs; len(ids) != 1 || ids[0] != "star-trek" {
		t.Errorf("BoxSetIDs = %v, want [star-trek] (stamped from the library + persisted)", ids)
	}
	if !got.Lineup[0].BoxSetsResolved {
		t.Error("BoxSetsResolved = false after a successful stamp — the entry will be re-fetched forever")
	}
}

// ⚠ The re-fetch guard, which is the entire reason BoxSetsResolved exists as a separate flag.
// A title in NO collection resolves to an empty slice, so a heal guarded on len(BoxSetIDs)==0
// would re-ask the library for it on every reconcile, for every such entry, forever — the N+1
// that stamping exists to prevent, reintroduced for the most common case. Verified by counting
// calls across two passes rather than by reading the guard.
func TestReconcile_BoxSetHealIsOneTimeEvenForNonMembers(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	res := &countingBoxSets{sets: map[provision.Key][]string{}} // in no collection at all
	e := newEngine(st, tun, avail, nil).WithBoxSets(res)

	seedChannel(t, st, "cbs2", 9, entry("movie:tmdb:1", "A"))
	for i := range 2 {
		if err := e.Reconcile(context.Background(), "cbs2"); err != nil {
			t.Fatalf("pass %d: %v", i+1, err)
		}
	}

	if res.calls != 1 {
		t.Errorf("resolver called %d times across 2 reconciles, want 1 — a non-member is being "+
			"re-fetched every pass (guard on BoxSetsResolved, not on len(BoxSetIDs))", res.calls)
	}
	got, _ := st.GetChannel(context.Background(), "cbs2")
	if !got.Lineup[0].BoxSetsResolved {
		t.Error("BoxSetsResolved = false for a resolved non-member — 'in no collection' must settle")
	}
}

// Backfill: a pending slot is pod-filled, then the title lands and an availability
// event places the real program IN PLACE, re-pushing the lineup.
func TestReconcile_BackfillOnAvailabilityEvent(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1"} // #2 not yet available
	e := newEngine(st, tun, avail, nil)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"), entry("movie:tmdb:2", "B"))

	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	// Only 1 program so far; slot 2 is pod-fill (flex on the wire).
	ch, _ := st.GetChannel(context.Background(), "c1")
	if got := programCount(ch); got != 1 {
		t.Fatalf("want 1 program before backfill, got %d", got)
	}
	pushesBefore := tun.Pushes

	// #2 lands. Emit the availability event.
	avail["movie:tmdb:2"] = "lib-2"
	e.OnAvailability(context.Background(), provision.DomainEvent{
		Key: "movie:tmdb:2", State: provision.Available,
	})

	ch, _ = st.GetChannel(context.Background(), "c1")
	if got := programCount(ch); got != 2 {
		t.Fatalf("want 2 programs after backfill, got %d", got)
	}
	if tun.Pushes <= pushesBefore {
		t.Fatalf("backfill did not re-push the lineup (pushes %d→%d)", pushesBefore, tun.Pushes)
	}
	// Placed in place: slot index 1 is B's program.
	if ch.Desired[1].Kind != schedule.SlotProgram || ch.Desired[1].LibraryItemID != "lib-2" {
		t.Errorf("B not placed in its own slot: %+v", ch.Desired[1])
	}
}

// Event-loss recovery (§9/§19): DROP the availability event entirely; the
// periodic sweep must still backfill from the store.
func TestSweep_RecoversFromLostEvent(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	e := newEngine(st, tun, avail, nil)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"), entry("movie:tmdb:2", "B"))

	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}

	// #2 lands, but we DO NOT emit the event (simulate a crash between event and
	// re-push, or a cross-replica in-memory event that never arrives).
	avail["movie:tmdb:2"] = "lib-2"

	// The reconcile set a future ReconcileDeadline; wind the clock past it so the
	// sweep claims the channel.
	now := time.Unix(1_800_000_000, 0).Add(20 * time.Minute)
	r := channels.NewRunner(e, st, time.Minute, 5*time.Minute, 50,
		func() time.Time { return now }, testkit.Logger())

	n := r.Sweep(context.Background())
	if n != 1 {
		t.Fatalf("sweep reconciled %d channels, want 1", n)
	}
	ch, _ := st.GetChannel(context.Background(), "c1")
	if got := programCount(ch); got != 2 {
		t.Fatalf("sweep did not backfill the lost event: %d programs, want 2", got)
	}
}

// The periodic sweep is the durable guarantee for internal channels too. It must be able to
// claim and materialize one on an install with no Programmer at all.
func TestSweep_MaterializesInternalChannelWithoutProgrammer(t *testing.T) {
	st := newStore(t)
	e := newEngineForBackend(st, nil, mapAvail{"movie:tmdb:1": "lib-1"}, nil,
		func() string { return schedule.PlayoutBackendInternal })
	seedChannel(t, st, "internal-sweep", 5, entry("movie:tmdb:1", "A"))

	now := time.Unix(1_800_000_000, 0).UTC()
	r := channels.NewRunner(e, st, time.Minute, 5*time.Minute, 50,
		func() time.Time { return now }, testkit.Logger())
	if n := r.Sweep(context.Background()); n != 1 {
		t.Fatalf("sweep reconciled %d internal channels, want 1", n)
	}
	ch, err := st.GetChannel(context.Background(), "internal-sweep")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Status != schedule.StatusLive || programCount(ch) != 1 {
		t.Fatalf("sweep did not materialize internal channel: status=%s desired=%+v", ch.Status, ch.Desired)
	}
}

// Drift substitution (§9/§19): a scheduled program vanishes from the library; the
// sweep flags the channel drifted and demotes the slot.
func TestSweep_FlagsDriftWhenProgramVanishes(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1", "movie:tmdb:2": "lib-2"}
	e := newEngine(st, tun, avail, nil)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"), entry("movie:tmdb:2", "B"))
	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}

	// #2 is deleted/re-id'd in the library.
	delete(avail, "movie:tmdb:2")

	now := time.Unix(1_800_000_000, 0).Add(20 * time.Minute)
	r := channels.NewRunner(e, st, time.Minute, 5*time.Minute, 50,
		func() time.Time { return now }, testkit.Logger())
	r.Sweep(context.Background())

	ch, _ := st.GetChannel(context.Background(), "c1")
	if ch.Status != schedule.StatusDrifted {
		t.Fatalf("status = %s, want drifted", ch.Status)
	}
	if ch.Desired[1].IsProgram() {
		t.Errorf("vanished program not demoted: %+v", ch.Desired[1])
	}
}

// Media-server freshness (§9), operation-specific: CREATING a channel triggers a
// tuner RE-SCAN (so the media server discovers the new channel — a guide refresh
// alone won't surface it, the bug found in the first live smoke); an idempotent
// no-op reconcile pokes nothing.
func TestReconcile_RescansTunerOnCreate(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	avail := mapAvail{"movie:tmdb:1": "lib-1"}
	guide := &fakeGuide{}
	e := newEngine(st, tun, avail, guide)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"))

	_ = e.Reconcile(context.Background(), "c1") // create
	if guide.rescans != 1 {
		t.Fatalf("create should re-scan the tuner once (discover new channel), got %d", guide.rescans)
	}
	if guide.pokes != 0 {
		t.Errorf("create should re-scan the tuner, not (only) poke the guide: pokes=%d", guide.pokes)
	}

	_ = e.Reconcile(context.Background(), "c1") // no-op
	if guide.rescans != 1 || guide.pokes != 0 {
		t.Errorf("no-op reconcile touched the media server: rescans=%d pokes=%d (want 1/0)", guide.rescans, guide.pokes)
	}
}

// Paused and detached are the two explicit opt-outs from reconciliation. Direct callers
// (not only the sweep claim) must leave the row untouched and return before consulting any
// playout dependency.
func TestReconcile_SkipsPausedAndDetachedWithoutMutation(t *testing.T) {
	for _, status := range []schedule.ChannelStatus{schedule.StatusPaused, schedule.StatusDetached} {
		t.Run(string(status), func(t *testing.T) {
			st := newStore(t)
			seedChannel(t, st, "inactive", 5, entry("movie:tmdb:1", "A"))
			ch, err := st.GetChannel(context.Background(), "inactive")
			if err != nil {
				t.Fatal(err)
			}
			ch.Status = status
			before, err := st.SaveChannel(context.Background(), ch)
			if err != nil {
				t.Fatal(err)
			}
			guide := &fakeGuide{}
			e := channels.New(st, nil, nil, guide, channels.Config{
				ResolvePlayoutBackendContext: func(context.Context) (string, error) {
					t.Fatal("inactive reconcile resolved the playout backend")
					return "", nil
				},
			}, nil, testkit.Logger())

			if err := e.Reconcile(context.Background(), "inactive"); err != nil {
				t.Fatal(err)
			}
			after, err := st.GetChannel(context.Background(), "inactive")
			if err != nil {
				t.Fatal(err)
			}
			if after.Revision != before.Revision {
				t.Fatalf("inactive reconcile mutated revision %d -> %d", before.Revision, after.Revision)
			}
			if guide.pokes != 0 || guide.rescans != 0 {
				t.Fatalf("inactive reconcile touched freshness: %d rescans/%d pokes",
					guide.rescans, guide.pokes)
			}
		})
	}
}

func TestReconcileReadsDurableBackendOnceAndFailsClosed(t *testing.T) {
	st := newStore(t)
	seedChannel(t, st, "active", 5, entry("movie:tmdb:1", "A"))
	before, err := st.GetChannel(context.Background(), "active")
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("checkpoint unavailable")
	reads := 0
	tun := testkit.NewTunarr()
	e := channels.New(st, tun, mapAvail{"movie:tmdb:1": "lib-1"}, nil, channels.Config{
		ResolvePlayoutBackendContext: func(context.Context) (string, error) {
			reads++
			return "", want
		},
	}, nil, testkit.Logger())

	if err := e.Reconcile(context.Background(), "active"); !errors.Is(err, want) {
		t.Fatalf("Reconcile error = %v, want checkpoint failure", err)
	}
	if reads != 1 {
		t.Fatalf("checkpoint reads = %d, want one per attempt", reads)
	}
	after, err := st.GetChannel(context.Background(), "active")
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || tun.Creates != 0 || tun.Pushes != 0 {
		t.Fatalf("checkpoint failure caused effects: revision %d -> %d, creates=%d pushes=%d",
			before.Revision, after.Revision, tun.Creates, tun.Pushes)
	}
}

// A guide-poke failure degrades freshness but never fails the reconcile (§9).
func TestReconcile_GuidePokeFailureIsNonFatal(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	guide := &fakeGuide{err: errPoke}
	e := newEngine(st, tun, mapAvail{"movie:tmdb:1": "lib-1"}, guide)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"))

	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatalf("guide poke failure must not fail the reconcile: %v", err)
	}
	ch, _ := st.GetChannel(context.Background(), "c1")
	if ch.Status != schedule.StatusLive {
		t.Errorf("channel should still be live despite poke failure, got %s", ch.Status)
	}
}

// programCount counts real program slots in a persisted channel's desired lineup.
func programCount(ch store.Channel) int {
	return schedule.DesiredLineup{Slots: ch.Desired}.ProgramCount()
}

// --- fakes ---

type fakeGuide struct {
	pokes   int // guide (EPG) refreshes — for existing-channel lineup changes
	rescans int // tuner re-scans — for new/removed channels (§9)
	err     error
}

func (f *fakeGuide) PokeGuideRefresh(context.Context) error {
	f.pokes++
	return f.err
}
func (f *fakeGuide) RescanTuner(context.Context) error {
	f.rescans++
	return f.err
}

var errPoke = errPokeType("poke boom")

type errPokeType string

func (e errPokeType) Error() string { return string(e) }

// ⚠ A channel that computes to NOTHING must not report `live`. This used to: statusFor
// returned live without looking at the deck, so a channel with a full lineup and zero
// airable slots read healthy while broadcasting nothing — and an empty grid was the
// operator's only symptom. That is how a seasonal-bench bug stayed invisible.
//
// Driven through the availability gate (no entry resolves to a library id) because it
// is the simplest way to empty a deck; the seasonal path that actually hit this in the
// wild is covered in internal/schedule.
func TestReconcile_EmptyDeckIsNotLive(t *testing.T) {
	st := newStore(t)
	tun := testkit.NewTunarr()
	// Entries exist on the lineup, but NOTHING is available, so no slot survives.
	e := newEngine(st, tun, mapAvail{}, nil)
	seedChannel(t, st, "c1", 5, entry("movie:tmdb:1", "A"), entry("movie:tmdb:2", "B"))

	if err := e.Reconcile(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	ch, _ := st.GetChannel(context.Background(), "c1")
	if ch.Status == schedule.StatusLive {
		t.Fatal("a channel with nothing to air reported live — the symptom that hid the bench bug")
	}
	if ch.Status != schedule.StatusEmpty {
		t.Fatalf("status = %s, want %s", ch.Status, schedule.StatusEmpty)
	}
}
