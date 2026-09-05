// Package reconcile is the provisioning backstop (design §4, §7, §18). A ticker
// claims due in-flight titles and, per title: retries `wanted` submissions,
// re-checks the library for arrivals, and enforces deadlines (past deadline →
// unavailable + downstream Cancel). This is why writes never client-retry (§6) —
// recovery lives here.
//
// ⚠ Two corrections, both made 2026-08-10 after this doc had outlived its facts.
// The library re-check is not a safety net for a missed inbound hook: the inbound
// arr hook was deleted and polling is now the ONLY way acquisition state arrives,
// so this loop is the mechanism, not the backstop for one. And retention no longer
// rides this ticker — the janitor was removed and became internal/retention, a
// scheduler job (§18.1). There is no janitor.go here; there has not been for some
// time, and a reader who went looking for one found nothing.
package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/requester"
	"github.com/loomarr/loomarr/internal/store"
)

// Emitter receives domain events for terminal transitions (§4 inv. 2); the
// scheduler consumes these (Phase 10). Nil is allowed (events just get logged).
type Emitter interface {
	Emit(ctx context.Context, ev provision.DomainEvent)
}

// Reconciler drives the provisioning backstop loop.
type Reconciler struct {
	store   store.TitleStore
	req     requester.Requester
	lib     LibrarySource
	emit    Emitter
	quality AcquisitionQuality
	log     *slog.Logger

	requestTTL     time.Duration // fresh deadline for a (re)submitted wanted title
	downloadingTTL time.Duration // deadline once downloading
	batch          int           // max titles claimed per tick
	lease          time.Duration // claim lease (§5); must exceed one tick's work
	now            func() time.Time
}

// AcquisitionQuality receives a terminal Title only after its row commit.
// Implementations own privacy, classification, and best-effort persistence.
type AcquisitionQuality interface {
	AcquisitionTerminal(context.Context, provision.Record)
}

// LibraryLookup is the narrow media-library seam used by one reconcile pass.
type LibraryLookup interface {
	Lookup(ctx context.Context, providerKind library.ProviderKind, providerID string, mediaType library.MediaType) (itemID string, present bool, err error)
}

// LibrarySource binds one immutable media-library operation. It is invoked once
// per Tick, before the claimed batch is reconciled.
type LibrarySource func() LibraryLookup

// Config parameterizes the reconciler (from §15).
type Config struct {
	RequestTTL     time.Duration
	DownloadingTTL time.Duration
	Batch          int
	Lease          time.Duration
}

// New builds a Reconciler. now defaults to time.Now.
func New(st store.TitleStore, req requester.Requester, lib LibraryLookup, emit Emitter, cfg Config, now func() time.Time, log *slog.Logger) *Reconciler {
	return NewDynamic(st, req, func() LibraryLookup { return lib }, emit, cfg, now, log)
}

// NewDynamic builds a Reconciler whose library source binds the complete live
// connection once for every Tick. Static callers should use New.
func NewDynamic(st store.TitleStore, req requester.Requester, lib LibrarySource, emit Emitter, cfg Config, now func() time.Time, log *slog.Logger) *Reconciler {
	if now == nil {
		now = time.Now
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 50
	}
	if cfg.Lease <= 0 {
		cfg.Lease = 2 * time.Minute
	}
	return &Reconciler{
		store: st, req: req, lib: lib, emit: emit, log: log,
		requestTTL: cfg.RequestTTL, downloadingTTL: cfg.DownloadingTTL,
		batch: cfg.Batch, lease: cfg.Lease, now: now,
	}
}

// WithQualityRecorder wires best-effort terminal acquisition measurement.
func (r *Reconciler) WithQualityRecorder(recorder AcquisitionQuality) *Reconciler {
	r.quality = recorder
	return r
}

// Tick runs one reconcile pass: claim the due batch and reconcile each. Safe to
// run concurrently across replicas — ClaimDueTitles leases rows so two ticks
// never process the same title (§5, §18). Returns the number reconciled.
func (r *Reconciler) Tick(ctx context.Context) (int, error) {
	now := r.now()
	due, err := r.store.ClaimDueTitles(ctx, now, r.lease, r.batch)
	if err != nil {
		return 0, err
	}
	if len(due) == 0 {
		return 0, nil
	}
	lib := r.lib()
	for _, rec := range due {
		r.reconcileOne(ctx, lib, rec, now)
	}
	return len(due), nil
}

// reconcileOne advances a single claimed record. The record was leased (its
// deadline pushed forward on claim), so this MUST write a real new deadline for
// still-in-flight titles, or the title would silently drop out of the due set
// until the lease lapses.
func (r *Reconciler) reconcileOne(ctx context.Context, lib LibraryLookup, rec provision.Record, now time.Time) {
	switch rec.State {
	case provision.Wanted:
		r.retryWanted(ctx, rec, now)
	case provision.Requested, provision.Downloading:
		// Deadline first: the record's *original* deadline governs give-up. It
		// was claimed because that deadline had passed, so give up now (§4).
		r.giveUp(ctx, lib, rec, now)
	default:
		// Terminal or unexpected — nothing to do (Apply would no-op anyway).
	}
}

// retryWanted re-submits a title whose downstream submission hasn't been
// accepted. On success → requested with a fresh request TTL; on failure it stays
// wanted with a pushed-out deadline so the next tick retries (backoff-ish).
func (r *Reconciler) retryWanted(ctx context.Context, rec provision.Record, now time.Time) {
	if err := r.req.Request(ctx, rec.Title); err != nil {
		// Still not accepted. Keep wanted; extend the deadline so we retry later.
		next, _ := provision.Apply(rec, provision.Event{Kind: provision.SubmitFailed, Err: err.Error()}, now)
		next.Deadline = now.Add(r.requestTTL)
		r.persist(ctx, next, nil)
		return
	}
	next, emitted := provision.Apply(rec, provision.Event{Kind: provision.RequestAccepted, Deadline: now.Add(r.requestTTL)}, now)
	r.persist(ctx, next, emitted)
}

// giveUp checks the library one last time (missed-webhook backstop, §7) before
// declaring a title unavailable. If the item is actually present, confirm it
// available instead; else past-deadline → unavailable + best-effort Cancel.
// now is the wall clock (for UpdatedAt); due-ness is proven by the claim.
func (r *Reconciler) giveUp(ctx context.Context, lib LibraryLookup, rec provision.Record, now time.Time) {
	// Missed-webhook re-check: maybe it landed and we never got the Import hook.
	if id, present, err := lookup(ctx, lib, rec.Title); err == nil && present {
		next, emitted := provision.Apply(rec, provision.Event{Kind: provision.LibraryConfirmed, LibraryID: id}, now)
		r.persist(ctx, next, emitted)
		return
	}
	// Not present → give up. Being in the claimed set already proves the row was
	// past its (original) deadline; ClaimDueTitles leased the deadline forward,
	// so we evaluate the give-up against the record's own (leased) deadline to
	// satisfy Apply's guard rather than the wall clock. Best-effort cancel first.
	if err := r.req.Cancel(ctx, rec.Title); err != nil {
		r.log.Warn("reconcile: cancel failed (continuing to unavailable)", "key", rec.Key, "err", err)
	}
	// Evaluate the guard against the record's (leased) deadline so Apply honors
	// the give-up, then correct UpdatedAt to the real wall clock for the audit
	// trail.
	next, emitted := provision.Apply(rec, provision.Event{Kind: provision.DeadlineExceeded, Err: "deadline exceeded"}, rec.Deadline)
	if next.State == provision.Unavailable {
		next.UpdatedAt = now
	}
	r.persist(ctx, next, emitted)
}

func lookup(ctx context.Context, lib LibraryLookup, t provision.Title) (string, bool, error) {
	kind := library.TMDB
	id := itoa(t.TMDBID)
	mt := library.Movie
	if t.MediaType == provision.Series {
		mt = library.Series
		if t.TVDBID > 0 {
			kind, id = library.TVDB, itoa(t.TVDBID)
		}
	}
	return lib.Lookup(ctx, kind, id, mt)
}

// persist writes the advanced record and emits any terminal domain events.
func (r *Reconciler) persist(ctx context.Context, rec provision.Record, emitted []provision.DomainEvent) {
	if err := r.store.UpsertTitle(ctx, rec); err != nil {
		r.log.Error("reconcile: persist", "key", rec.Key, "err", err)
		return
	}
	if len(emitted) > 0 && r.quality != nil {
		r.quality.AcquisitionTerminal(ctx, rec)
	}
	for _, ev := range emitted {
		r.log.Info("provision event", "key", ev.Key, "state", ev.State)
		if r.emit != nil {
			r.emit.Emit(ctx, ev)
		}
	}
}
