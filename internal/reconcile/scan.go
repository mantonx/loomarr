package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/activity"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/store"
)

// LibraryScan is the poll-based availability path (design §4, §18.1) — the PRIMARY way a
// requested title reaches `available`, mirroring how Overseerr/Seerr work (poll the library,
// don't wait on a webhook). Where the reconciler's giveUp does a per-title Lookup only for
// deadline-due records, the scan lists what the media server recently added in ONE call and
// confirms every in-flight title now present. Same LibraryConfirmed → available transition,
// but continuous and not deadline-gated, so availability lands promptly without the inbound
// webhook (which is retired once this is proven).
type LibraryScan struct {
	store   store.TitleStore
	scanner library.LibraryScanner
	emit    Emitter
	quality AcquisitionQuality
	now     func() time.Time
	log     *slog.Logger
	// activity records Dashboard feed lines (§12, V32). Optional: nil-safe on the Recorder,
	// so a scan built without one (unit tests) simply records nothing.
	activity *activity.Recorder

	// lookback bounds the incremental RecentlyAdded window. It intentionally exceeds the scan
	// interval (a few multiples) so a briefly-missed tick — a slow scan, a restart — still
	// re-observes recent imports rather than dropping them into the daily full-sweep gap.
	lookback time.Duration
}

// NewLibraryScan builds a scan. now defaults to time.Now; lookback defaults to 1h (comfortably
// wider than the default 5-minute scan cadence).
func NewLibraryScan(st store.TitleStore, scanner library.LibraryScanner, emit Emitter, lookback time.Duration, now func() time.Time, log *slog.Logger) *LibraryScan {
	if now == nil {
		now = time.Now
	}
	if lookback <= 0 {
		lookback = time.Hour
	}
	return &LibraryScan{store: st, scanner: scanner, emit: emit, now: now, lookback: lookback, log: log}
}

// WithActivity wires the Dashboard feed recorder (§12, V32). Optional and chainable, matching
// how the scheduler takes its notifier: a scan without one records nothing rather than
// forcing every test to construct a recorder it does not assert on.
func (s *LibraryScan) WithActivity(r *activity.Recorder) *LibraryScan { s.activity = r; return s }

// WithQualityRecorder wires best-effort terminal acquisition measurement.
func (s *LibraryScan) WithQualityRecorder(recorder AcquisitionQuality) *LibraryScan {
	s.quality = recorder
	return s
}

// Incremental confirms availability for in-flight titles added to the library within the
// lookback window — the frequent (5-minute) job. Returns the number of titles confirmed.
func (s *LibraryScan) Incremental(ctx context.Context) (int, error) {
	inflight, err := s.inflightByKey(ctx)
	if err != nil || len(inflight) == 0 {
		return 0, err
	}
	since := s.now().Add(-s.lookback)
	items, err := s.scanner.RecentlyAdded(ctx, since)
	if err != nil {
		return 0, err
	}
	return s.confirm(ctx, items, inflight)
}

// Full confirms availability against the ENTIRE library — the periodic safety net (daily) for
// anything the incremental window missed (Loomarr down across a scan, a late-attached provider
// id on an older item). Returns the number of titles confirmed.
func (s *LibraryScan) Full(ctx context.Context) (int, error) {
	inflight, err := s.inflightByKey(ctx)
	if err != nil || len(inflight) == 0 {
		return 0, err
	}
	items, err := s.scanner.AllItems(ctx)
	if err != nil {
		return 0, err
	}
	return s.confirm(ctx, items, inflight)
}

// confirm correlates scanned library items against the in-flight title set and applies
// LibraryConfirmed to any match. It indexes the (small) in-flight set by provision.Key, then
// probes it with each (potentially many) scanned item's key — the key parity guarantee (same
// Key from a Title, a webhook, or a scan item) makes the match exact. O(items) probes.
func (s *LibraryScan) confirm(ctx context.Context, items []library.SearchResult, inflight map[provision.Key]provision.Record) (int, error) {
	now := s.now()
	confirmed := 0
	seen := make(map[provision.Key]bool, len(inflight)) // one confirm per title per scan
	for _, it := range items {
		// A library item can carry BOTH a TVDB and a TMDB id (Emby exposes both for a series),
		// so probe the in-flight set under EVERY key the item can produce. A record keyed by
		// one namespace (e.g. a TMDB-only series, `series:tmdb:<id>`) then still matches the
		// same show the library indexed under the other (`series:tvdb:<id>`) — without this,
		// a TMDB-keyed series never confirms `available` even once its episodes are present.
		for _, key := range ScanItemKeys(it) {
			rec, awaiting := inflight[key]
			if !awaiting || seen[key] {
				continue
			}
			seen[key] = true
			next, emitted := provision.Apply(rec, provision.Event{Kind: provision.LibraryConfirmed, LibraryID: it.LibraryItemID}, now)
			s.persist(ctx, next, emitted)
			confirmed++
			break // one confirm per scanned item
		}
	}
	return confirmed, nil
}

// inflightByKey loads every title awaiting the library (requested + downloading) indexed by
// key. These are the only states a library confirmation can advance; wanted has no release yet
// and terminal states are frozen (provision invariant 1).
func (s *LibraryScan) inflightByKey(ctx context.Context) (map[provision.Key]provision.Record, error) {
	out := make(map[provision.Key]provision.Record)
	for _, st := range []provision.State{provision.Requested, provision.Downloading} {
		recs, err := s.store.ListTitlesByState(ctx, st)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			out[r.Key] = r
		}
	}
	return out, nil
}

// persist writes the confirmed record and fans its terminal events to the emitter — identical
// to reconcile's persist so both availability paths behave the same downstream.
func (s *LibraryScan) persist(ctx context.Context, rec provision.Record, emitted []provision.DomainEvent) {
	if err := s.store.UpsertTitle(ctx, rec); err != nil {
		s.log.Error("library-scan: persist", "key", rec.Key, "err", err)
		return
	}
	if len(emitted) > 0 && s.quality != nil {
		s.quality.AcquisitionTerminal(ctx, rec)
	}
	for _, ev := range emitted {
		s.log.Info("provision event", "key", ev.Key, "state", ev.State, "src", "library-scan")
		if s.emit != nil {
			s.emit.Emit(ctx, ev)
		}
		// The Dashboard feed (§12, V32). Written HERE rather than off the event bus: the bus
		// is lossy by design, and it carries `{type:"title"}` where the feed needs "Die Hard
		// landed — ready to schedule". Only this point knows the title.
		//
		// Only `available` earns a line. Every intermediate transition would turn a five-row
		// glance into a log, and the operator's question is "did it arrive?", not "what state
		// is it in now?".
		if ev.State == provision.Available {
			s.activity.Info(ctx, store.ActivityKindTitle, string(ev.Key),
				titleLabel(rec)+" landed — ready to schedule")
		}
	}
}

// titleLabel is what a human calls the title, falling back to the key when the record has no
// name — a feed line reading "landed" with nothing in front of it is worse than an ugly one.
func titleLabel(rec provision.Record) string {
	if rec.Title.Name != "" {
		return rec.Title.Name
	}
	return string(rec.Key)
}

// ScanItemKeys builds EVERY provision.Key a scanned library item can be identified by, via the
// same Title.Key() path the store used to key the record — so the match is byte-for-byte in each
// namespace. A library item often carries more than one provider id (Emby exposes both Tvdb and
// Tmdb for a series), and a title record is keyed by whichever id it was born with — TMDB for a
// suggester/channel-add series (no TVDB id yet), TVDB once known. Producing a key per id and
// probing the in-flight set under each closes that gap: a `series:tmdb:<id>` record still matches
// the same show the library indexed as `series:tvdb:<id>`. Order is TVDB-first (the library's
// preferred series identity), then TMDB; deduped. Empty when the item carries no usable id.
// Exported because the BoxSet membership index (app.libraryBoxSets, programming-design §2.2)
// faces the identical problem: a collection member carrying both provider ids must be findable
// under whichever key form the lineup entry was born with. A second derivation would be a
// fifth copy of this logic that drifts by one namespace and silently matches nothing.
func ScanItemKeys(it library.SearchResult) []provision.Key {
	mt := provision.Movie
	if it.MediaType == library.Series {
		mt = provision.Series
	}
	keys := make([]provision.Key, 0, 2)
	seen := make(map[provision.Key]struct{}, 2)
	add := func(t provision.Title) {
		if k, err := t.Key(); err == nil {
			if _, dup := seen[k]; !dup {
				seen[k] = struct{}{}
				keys = append(keys, k)
			}
		}
	}
	// TVDB-preferred key (series only — Title.Key() picks tvdb when TVDBID>0).
	add(provision.Title{MediaType: mt, TMDBID: it.TMDBID, TVDBID: it.TVDBID})
	// The TMDB key explicitly, so a TMDB-keyed record matches even when the item also has a
	// TVDB id (which Title.Key() would otherwise prefer, hiding the TMDB form).
	if it.TMDBID > 0 {
		add(provision.Title{MediaType: mt, TMDBID: it.TMDBID})
	}
	return keys
}
