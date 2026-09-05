package channels

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/programmer"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
)

// Reconcile materializes one channel's durable desired lineup (§9). For a
// Tunarr-backed channel it additionally brings Tunarr's actual state in line with
// that desired result; an internal-playout channel makes no Programmer calls. It
// is idempotent and minimal-diff. Safe to call from the API, the sweep, or a
// backfill event. Serialized per channel by the mutex (§18).
//
// Steps:
//  1. Load the channel; skip if detached.
//  2. Recompute desired from the approved lineup + current availability (pure).
//  3. Revalidate program slots against the library — a vanished program demotes
//     to a placeholder and flags the channel drifted (§9 slot revalidation).
//  4. For a Tunarr-backed channel only, ensure the remote channel exists and diff
//     + push its lineup. Internal playout consumes the local desired state directly.
//  6. Persist the (possibly updated) channel: new TunarrID, desired snapshot,
//     status, next reconcile deadline.
//  7. Nudge the media server after committed local or Tunarr changes (best-effort,
//     §9): first materialization/backend switches re-scan the tuner; a desired-only
//     change refreshes guide data.
func (e *Engine) Reconcile(ctx context.Context, channelID string) (err error) {
	return e.reconcile(ctx, channelID, reconcileOptions{observeProposalQuality: true})
}

// PrepareInheritedBackend materializes every active channel that inherits the global
// playout backend as though target were already applied. It is the channel-domain half
// of an ordered global backend transition: callers can prepare the fleet first, then
// publish the new applied setting and rewire the media server. Explicit per-channel
// backend pins remain authoritative and are not reconciled by this fleet operation.
//
// Channel failures are independent. The method continues preparing the remaining fleet
// and returns their errors joined together, so retrying the operation converges only the
// unfinished work through Reconcile's existing idempotent, minimal-diff path.
func (e *Engine) PrepareInheritedBackend(ctx context.Context, target string) error {
	target = schedule.NormalizePlayoutBackend(target)
	if target != schedule.PlayoutBackendInternal && target != schedule.PlayoutBackendTunarr {
		return fmt.Errorf("prepare inherited channels: invalid playout backend %q", target)
	}

	all, err := e.store.ListChannels(ctx)
	if err != nil {
		return fmt.Errorf("prepare inherited channels: list channels: %w", err)
	}

	var errs []error
	for _, ch := range all {
		if !ch.Status.Reconcilable() || schedule.HasExplicitPlayoutBackend(ch.Policy) {
			continue
		}
		if err := e.reconcile(ctx, ch.ID, reconcileOptions{
			globalBackend: target,
			inheritedOnly: true,
		}); err != nil {
			errs = append(errs, fmt.Errorf("prepare inherited channel %s: %w", ch.ID, err))
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(errs...)
}

type reconcileOptions struct {
	// globalBackend is an explicit transition target when inheritedOnly is true.
	// It stays fixed across CAS retries; ordinary Reconcile deliberately continues
	// resolving the currently applied backend once per fresh row attempt.
	globalBackend          string
	inheritedOnly          bool
	observeProposalQuality bool
}

func (e *Engine) reconcile(ctx context.Context, channelID string, opts reconcileOptions) (err error) {
	lock := e.lockFor(channelID)
	lock.Lock()
	defer lock.Unlock()

	// §17 reconcile-loop latency + channel-reconcile counter. e.now() is the
	// injected clock, so the duration is deterministic (0) under a fixed-time test.
	start := e.now()
	defer func() {
		if e.metrics != nil {
			e.metrics.ChannelReconciled(e.now().Sub(start), err == nil)
		}
	}()

	// A reconcile performs remote work from a local channel snapshot. An approval or
	// operator edit may legitimately advance that snapshot while the remote calls are
	// in flight, so the final SaveChannel is a compare-and-swap. Losing that CAS is a
	// request to reload and converge from the new truth, not permission to overwrite it.
	// Keep the retry here, behind the Reconcile seam, so API, sweep and availability
	// callers cannot accidentally implement different stale-write behaviour.
	state := reconcileRun{}
	defer func() {
		if !opts.observeProposalQuality || e.quality == nil || !state.qualityEligible {
			return
		}
		duration := e.now().Sub(start)
		if err != nil {
			job, qualityErr := e.store.GetJob(ctx, state.qualityJobID)
			if qualityErr != nil {
				if e.log != nil {
					e.log.Warn("recheck Proposal Job scheduling milestone", "err", qualityErr)
				}
				return
			}
			if job.ReachedLive {
				return
			}
			e.quality.ProposalSchedulingFailed(ctx, state.qualityJobID, e.now(), duration)
		} else if state.qualityScheduled {
			e.quality.ProposalScheduled(ctx, state.qualityJobID, e.now(), duration)
		}
	}()

	const maxAttempts = 4
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = e.reconcileOnce(ctx, channelID, &state, opts)
		if !errors.Is(err, store.ErrChannelStale) && !errors.Is(err, store.ErrChannelConflict) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return fmt.Errorf("reconcile channel %s conflicted during %d attempts: %w",
		channelID, maxAttempts, store.ErrChannelStale)
}

// reconcileRun carries remote effects across a stale-snapshot restart. A Tunarr
// channel created on attempt one still requires a tuner rescan after attempt two
// commits, even though the durable Tunarr id makes attempt two look like an update.
type reconcileRun struct {
	channelAffecting   bool
	channelListChanged bool
	qualityEligible    bool
	qualityScheduled   bool
	qualityJobID       string
}

func (e *Engine) reconcileOnce(
	ctx context.Context,
	channelID string,
	run *reconcileRun,
	opts reconcileOptions,
) error {

	ch, err := e.store.GetChannel(ctx, channelID)
	if err != nil {
		return fmt.Errorf("load channel %s: %w", channelID, err)
	}
	if !ch.Status.Reconcilable() {
		return nil // detached = no longer managed (§9 ownership); paused = deliberately off the sweep
	}
	if opts.observeProposalQuality {
		run.qualityEligible = false
		run.qualityScheduled = false
		run.qualityJobID = ""
		if ch.IntentRef != "" {
			job, qualityErr := e.store.GetJob(ctx, ch.IntentRef)
			if qualityErr != nil {
				if e.log != nil {
					e.log.Warn("read Proposal Job scheduling milestone", "err", qualityErr)
				}
			} else if !job.ReachedLive {
				run.qualityEligible = true
				run.qualityJobID = ch.IntentRef
			}
		}
	}
	if opts.inheritedOnly && schedule.HasExplicitPlayoutBackend(ch.Policy) {
		return nil // a concurrent operator pin wins over a fleet-default transition
	}
	previousStatus := ch.Status
	// Resolve ONCE for this attempt from the same channel snapshot all work below uses.
	// A policy edit that changes the backend advances the row revision, so the final
	// SaveChannel CAS restarts from that new truth. The global value is live and not
	// row-versioned; resolving once prevents one attempt from straddling two backends.
	globalBackend := opts.globalBackend
	if !opts.inheritedOnly {
		globalBackend, err = e.playoutBackendFor(ctx)
		if err != nil {
			return fmt.Errorf("resolve playout backend for channel %s: %w", channelID, err)
		}
	}
	playsInternally := schedule.PlaysInternally(ch.Policy, globalBackend)

	// Heal each entry's metadata in place through the one lineup primitive (§9 LineupHeal):
	// fill an empty OfficialRating (§389 — a fail-closed audience gate would otherwise drop a
	// now-in-library title to dead air) and settle each movie's TMDB CollectionID (§5 franchise
	// ordering). Bounded, one-time lookups; the healed values are stamped onto ch.Lineup and
	// persisted by the SaveChannel below, so a future reconcile skips them. Membership + order
	// are untouched — heal only enriches.
	ch.Lineup = schedule.ApplyLineup(ch.Lineup, nil, schedule.LineupHeal,
		schedule.ApplyOpts{Enrich: e.healEntry(ctx)})

	// 1b: decide whether the selected backend has clips to fill a break (§10). Internal
	// playout asks the backend-independent HasPool question: its clips are local files and
	// deliberately need no Tunarr uuid. Tunarr asks BuildFillerList once and carries those
	// same ids to the projection below. This is the distinction PodFiller's two-method
	// interface exists to preserve; using BuildFillerList on internal-only installs made a
	// real local catalog look empty.
	var fillerIDs []string
	hasFillerPool := false
	var playableFillerMs int64
	if e.pods != nil {
		seed, selection := PodSeed(ch.ID), SelectionForChannel(ch)
		if playsInternally {
			playableFillerMs = e.pods.PlayableDurationMs(ctx, ch.ID, seed, selection)
			hasFillerPool = playableFillerMs > 0
		} else if ids, ok := e.pods.BuildFillerList(ctx, ch.ID, seed, selection); ok && len(ids) > 0 {
			fillerIDs, hasFillerPool = ids, true
		}
	}

	// 2: recompute desired from the approved lineup + current availability under the
	// channel's ChannelPolicy (programming-design §3–§7). ComputeDesiredAt resolves
	// each entry against the library (a vanished title comes back a placeholder),
	// applies the audience/scope/seasonal filters, and orders with separation. Apply
	// the global commercial-break density (§10) so breaks are interleaved, and pass
	// the wall-clock so seasonality (§6) evaluates against the container TZ.
	//
	// Breaks are only interleaved when the selected backend has a filler pool: inserting
	// gaps with no clips is a promise of commercials we cannot keep. No pool means
	// programs play back-to-back. Once clips land, the next reconcile re-inserts breaks.
	chDomain := ch.Channel
	chDomain.LastAired = e.lastAiredFor(ctx, ch.ID)
	chDomain.BreaksPerHour = BreaksPerHourFor(ch.Policy, hasFillerPool, e.breaksPerHourFor())
	chDomain.BreakDurationMs = BreakDurationFor(ch.Policy, e.breakDurationFor()).Milliseconds()
	chDomain.DefaultWindow = e.defaultWindowFor() // §6.5 rolling-window horizon from settings
	desired := schedule.ComputeDesiredAt(chDomain, ch.Lineup, e.avail, e.policy, ch.Policy, e.now())
	if playsInternally {
		desired.Slots = capCommercialBreaks(desired.Slots, playableFillerMs)
	}
	// Building is the durable "this backend must (re)publish the channel list" marker:
	// it covers first materialization and a backend switch that reuses a historical
	// Tunarr id. An internal Empty channel also enters the surfable M3U catalog when its
	// first program becomes available, even though no PATCH marked it Building. Other
	// desired changes on an already-settled channel affect only guide data. These facts
	// stay attempt-local so a stale SaveChannel publishes no freshness; the retry
	// recomputes them from the winning row.
	desiredChangedLocally := !slices.Equal(ch.Desired, desired.Slots)

	// Record the relaxation-ladder steps this pass applied (§7) back onto the
	// channel's policy so the UI surfaces them; recomputed from scratch each
	// reconcile, so a recovered pool automatically un-relaxes to an empty list.
	// WithApplied is the reconcile-only writer — it touches Applied and nothing else (§2.1).
	ch.Policy = ch.Policy.WithApplied(desired.Applied)

	// Filler is not inlined into desired. An internal encoder resolves each SlotFiller
	// at airtime; Tunarr receives it as FLEX and fills it from the attached list below.
	// Both therefore persist the same gap shape rather than mutating `desired` here.

	// 3: drift detection (§9 slot revalidation) is a comparison against what we
	// *previously* scheduled: a slot that was a real program in the persisted
	// desired and is no longer a program now means a scheduled item vanished
	// (deleted / re-id'd). An old `available` is never trusted forever — surface
	// it as StatusDrifted so the Channels view flags it.
	staleCount := staleProgramCount(ch.Desired, desired.EligibleKeys)
	drifted := staleCount > 0
	if e.metrics != nil {
		e.metrics.ChannelSlotSubstitutions(staleCount)
	}
	nextStatus := e.statusFor(desired, drifted)
	// Anchor a new channel at the instant its first playable deck becomes live.
	// Once set, every later reconcile and backend transition preserves it.
	if ch.PlayoutAnchor.IsZero() && (nextStatus == schedule.StatusLive || nextStatus == schedule.StatusDrifted) {
		ch.PlayoutAnchor = e.now().UTC().Truncate(time.Second)
	}
	// Empty internal channels are absent from the tuner M3U. Crossing that membership
	// edge in either direction therefore needs the stronger tuner re-scan, not merely a
	// guide refresh. The reverse edge matters when a policy change filters a live channel
	// down to no playable programs: without it, the media server retains a dead channel
	// until its own periodic tuner scan.
	internalCatalogMembershipChanged := playsInternally &&
		(ch.Status == schedule.StatusEmpty) != (nextStatus == schedule.StatusEmpty)
	channelListChangedLocally := ch.Status == schedule.StatusBuilding || internalCatalogMembershipChanged

	// 4–5 are the optional Tunarr PROJECTION. Internal playout consumes the local
	// schedule directly through CyclePreview and must not touch Programmer, including a
	// historical TunarrID retained from an earlier backend choice. Keeping that id and
	// remote row intact makes a backend switch reversible; only explicit purge deletes it.
	if !playsInternally {
		spec := programmer.ChannelSpec{
			TunarrID: ch.TunarrID,
			Number:   ch.Number,
			Name:     ch.Name,
			Group:    ch.Group,
			Logo:     ch.Logo,
		}
		storedNumber := ch.Number
		tunarrID, usedNumber, createdRemote, err := e.ensureChannel(ctx, ch.ID, spec)
		if err != nil {
			return err
		}
		// A create can land on a different number than requested when Tunarr already occupied it
		// (§9 V54). Take it: leaving `ch.Number` at the wanted value would make Loomarr's guide, the
		// XMLTV it publishes, and Tunarr disagree about where the channel is.
		if usedNumber != ch.Number {
			ch.Number = usedNumber
			run.channelAffecting = true
		}
		if createdRemote || tunarrID != ch.TunarrID {
			oldTunarrID := ch.TunarrID
			ch.TunarrID = tunarrID
			run.channelAffecting = true   // created (or recreated after out-of-band delete)
			run.channelListChanged = true // the media server must re-scan the tuner to discover it

			// CHECKPOINT the new id NOW, before the lineup push (which can fail). This is a
			// targeted compare-and-swap on the old Tunarr id rather than a full-row save:
			// an operator edit that landed during the remote create must survive, while the
			// newly-created remote identity must become durable so a restart never creates
			// a second shell. Attach increments the row revision. The ordinary +1 result
			// means our snapshot is still current; a larger result means another writer
			// committed first, so the attachment is durable but every other planned field
			// is stale and must be recomputed before pushing a lineup.
			priorRevision := ch.Revision
			revision, aerr := e.store.AttachTunarrChannel(
				ctx, ch.ID, oldTunarrID, tunarrID, storedNumber, ch.Number,
			)
			if aerr != nil {
				// Per-process channel locks do not serialize Postgres replicas. Two replicas may
				// therefore create different remote channels before either checkpoint wins. If
				// another replica attached its id first, this create is ours but is not durable
				// anywhere; delete it before replanning so Tunarr does not accumulate an orphan.
				if createdRemote && (errors.Is(aerr, store.ErrChannelStale) || errors.Is(aerr, store.ErrChannelConflict)) {
					e.discardUnattachedCreate(ctx, channelID, tunarrID)
				}
				return fmt.Errorf("checkpoint Tunarr id for channel %s: %w", channelID, aerr)
			}
			ch.Revision = revision
			if revision != priorRevision+1 {
				return store.ErrChannelStale
			}
		}

		// 4b: attach the channel's Tunarr filler-list from the clip pool computed in step 1b,
		// now that the channel exists (TunarrID is set). Tunarr plays these clips into the flex
		// gaps SetLineup leaves between programs. When there's no pool (hasFillerPool false),
		// this detaches any stale list AND the lineup above already omitted the breaks, so the
		// channel plays back-to-back with no empty flex. Best-effort: a filler failure never
		// fails the reconcile (§9 resilience); the next sweep retries.
		if e.pods != nil {
			e.attachFillerList(ctx, ch, fillerIDs)
		}

		// 5: diff desired lineup vs actual; push only on a difference.
		actual, err := e.prog.GetLineup(ctx, ch.TunarrID)
		if err != nil {
			return fmt.Errorf("read lineup %s: %w", ch.TunarrID, err)
		}
		if lineupDiffers(desired.Slots, actual) {
			if err := e.prog.SetLineup(ctx, ch.TunarrID, desired.Slots); err != nil {
				return fmt.Errorf("push lineup %s: %w", ch.TunarrID, err)
			}
			run.channelAffecting = true
		}
	}

	// 6: persist. Status reflects drift; a channel with any real program is live.
	ch.Desired = desired.Slots
	ch.Status = nextStatus
	reconcileTTL := e.reconcileTTLFor()
	if reconcileTTL <= 0 {
		reconcileTTL = 10 * time.Minute
	}
	ch.ReconcileDeadline = e.now().Add(reconcileTTL)
	ch.UpdatedAt = e.now().Unix()
	committed, err := e.store.SaveChannel(ctx, ch)
	if err != nil {
		return fmt.Errorf("persist channel %s: %w", channelID, err)
	}
	ch = committed
	if run.qualityEligible && (ch.Status == schedule.StatusLive || ch.Status == schedule.StatusDrifted) {
		run.qualityScheduled = true
	}
	// A shared encoder is reading the previously accepted cycle until it is retired.
	// Guide freshness alone cannot switch its current playout session: live proof was a
	// newly constrained Simpsons guide advertising S10 while the Shield reattached to a
	// nine-minute-stale pre-edit stream. Stop only after the replacement Desired cycle is
	// durable, and only when it actually changed; the next request starts at the correct
	// wall-clock offset. Postgres lifecycle invalidations repeat this on peer replicas.
	if playsInternally && desiredChangedLocally && e.scheduleInvalidator != nil {
		e.scheduleInvalidator.StopChannel(ch.ID)
	}

	// Tell the UI the channel changed so it updates live (no manual refresh — the
	// "self-maintaining" model, §9). Best-effort: nil notifier / a dropped frame is a
	// latency concern, never a correctness one (GET /v1/channels is the truth on load).
	if e.notify != nil {
		e.notify.ChannelChanged(ch.ID, string(previousStatus), string(ch.Status))
	}

	// 7: media-server freshness (best-effort; never fails reconcile). A committed local
	// Building marker changes the tuner list for either backend. Retry-carried flags,
	// however, describe Tunarr effects and count only when the winning attempt is still
	// Tunarr-backed; a stale remote create must not leak into an internal retry.
	channelListChanged := channelListChangedLocally
	channelAffecting := desiredChangedLocally
	if !playsInternally {
		channelListChanged = channelListChanged || run.channelListChanged
		channelAffecting = channelAffecting || run.channelAffecting
	}
	if channelListChanged {
		e.rescanTuner(ctx, channelID)
	} else if channelAffecting {
		e.pokeGuide(ctx, channelID)
	}
	return nil
}

// discardUnattachedCreate removes a remote channel this reconcile just created only when the
// durable row proves that this id did not attach. A read failure is deliberately hands-off:
// without a current row, deleting could remove the only recoverable remote channel after a
// transient database error. An empty current id is proof of non-attachment and is safe to clean.
func (e *Engine) discardUnattachedCreate(ctx context.Context, channelID, createdTunarrID string) {
	current, err := e.store.GetChannel(ctx, channelID)
	if err != nil || current.TunarrID == createdTunarrID {
		return
	}
	if err := e.prog.DeleteChannel(ctx, createdTunarrID); err != nil && e.log != nil {
		e.log.Error("failed to remove Tunarr channel that lost an attachment race",
			"channel", channelID, "orphanTunarrId", createdTunarrID,
			"attachedTunarrId", current.TunarrID, "err", err)
	}
}

// Purge fully removes a channel: it deletes the Tunarr channel (if one was ever
// pushed) and then hard-deletes the store row — the `?purge=true` path of DELETE
// /v1/channels/{id} (§7), as opposed to the default detach (which keeps both). It
// takes the per-channel lock so a concurrent reconcile can't race the deletion, and
// the Tunarr delete is idempotent (a 404 is already-gone). A channel that was never
// reconciled has no TunarrID, so only the store row is removed. A historical id with
// no Programmer fails closed rather than orphaning the remote channel, and every
// successful hard delete re-scans the media-server tuner list.
func (e *Engine) Purge(ctx context.Context, channelID string) error {
	lock := e.lockFor(channelID)
	lock.Lock()
	defer lock.Unlock()

	ch, err := e.store.GetChannel(ctx, channelID)
	if err != nil {
		return fmt.Errorf("load channel %s: %w", channelID, err)
	}
	if ch.TunarrID != "" && e.prog == nil {
		// The row is the only durable record of the remote identity. Deleting it when
		// no Programmer can remove the projection would manufacture an unmanaged orphan
		// that no later reconcile can discover or clean up.
		return fmt.Errorf("delete tunarr channel %s: programmer unavailable", ch.TunarrID)
	}
	if ch.TunarrID != "" {
		if derr := e.prog.DeleteChannel(ctx, ch.TunarrID); derr != nil {
			return fmt.Errorf("delete tunarr channel %s: %w", ch.TunarrID, derr)
		}
	}
	if err := e.store.DeleteChannel(ctx, channelID, ch.Revision); err != nil {
		return fmt.Errorf("delete channel %s: %w", channelID, err)
	}
	// The hard delete removes the channel from Loomarr's own M3U as well as any
	// Tunarr projection above. Either way the media server must re-read the tuner
	// list; a guide-only refresh cannot remove a channel.
	e.rescanTuner(ctx, channelID)
	return nil
}

// attachFillerList hands the channel's matched clip-pool program uuids (assembled once
// in reconcile step 1b) to the Programmer, which builds + attaches the channel's Tunarr
// filler-list (§10). Tunarr plays those clips into the flex gaps the scheduler leaves
// between programs. An empty ids (no pool) DETACHES any stale list — and since the lineup
// also omits breaks when there's no pool (step 2), the channel plays back-to-back rather
// than leaving empty flex. Best-effort: a filler error is logged, never returned — the
// channel still plays (§9 resilience), and EnsureFillerList is internally idempotent so a
// stable pool makes no Tunarr write.
func (e *Engine) attachFillerList(ctx context.Context, ch store.Channel, ids []string) {
	if err := e.prog.EnsureFillerList(ctx, ch.TunarrID, ids); err != nil && e.log != nil {
		e.log.Warn("attach filler list (channel plays without commercials this pass)",
			"channel", ch.ID, "err", err)
	}
}

// healEntry returns the per-entry enrichment schedule.ApplyLineup(LineupHeal) applies to each
// lineup entry in place (the caller persists ch.Lineup). It does two one-time repairs:
//   - fill an empty OfficialRating by looking the title up against the library (§389): a
//     fail-closed audience gate would otherwise drop a now-in-library title to dead air.
//   - settle a MOVIE's TMDB CollectionID tri-state (§5 franchise ordering): >0 a real
//     collection, -1 a resolved standalone (never re-fetched); series are skipped.
//   - stamp media-server collection membership for scope.collections (§2.2), settled by the
//     BoxSetsResolved flag so "in no collection" is answered once, not re-asked each pass.
//
// Bounded + best-effort by design (§9 resilience): only empty/unresolved fields are looked up
// (a stamped channel makes ZERO calls), a not-yet-present result leaves the field to retry next
// pass, and a lookup error never fails the reconcile — it just leaves the field unhealed.
// A nil source (ratings/franchises not wired) skips that half.
func (e *Engine) healEntry(ctx context.Context) func(*schedule.LineupEntry) {
	return func(en *schedule.LineupEntry) {
		if e.ratings != nil && en.OfficialRating == "" {
			if raw, ok, err := e.ratings.Rating(ctx, en.Key); err != nil {
				if e.log != nil {
					e.log.Warn("heal rating (entry stays unrated this pass)", "key", en.Key, "err", err)
				}
			} else if ok {
				en.OfficialRating = schedule.NormalizeRating(raw)
			}
		}
		// Stamp media-server collection membership (§2.2) so scope.collections enforces with
		// no library I/O. ⚠ Guarded on BoxSetsResolved, NOT on len(BoxSetIDs) == 0: a title in
		// no collection resolves to an empty slice, so an emptiness check would re-fetch the
		// most common case on every pass forever — the N+1 the stamping exists to prevent.
		if e.boxSets != nil && !en.BoxSetsResolved {
			if ids, ok, err := e.boxSets.BoxSets(ctx, en.Key); err != nil {
				if e.log != nil {
					e.log.Warn("heal boxsets (entry unresolved this pass)", "key", en.Key, "err", err)
				}
			} else if ok {
				en.BoxSetIDs = ids
				en.BoxSetsResolved = true
			}
		}
		if e.franchises != nil && en.CollectionID == 0 && !en.Key.IsSeries() {
			if id, ok, err := e.franchises.Collection(ctx, en.Key); err != nil {
				if e.log != nil {
					e.log.Warn("heal franchise (entry unresolved this pass)", "key", en.Key, "err", err)
				}
			} else if ok {
				// Settle the tri-state: a real collection id, or -1 for a resolved standalone so
				// it's never re-fetched (0 alone can't distinguish "standalone" from "unresolved").
				if id > 0 {
					en.CollectionID = id
				} else {
					en.CollectionID = -1
				}
			}
		}
	}
}

// SelectionForChannel translates a channel's persisted filler policy (§10
// FillerSelection) into the filler-package Selection the assembler consumes — the
// boundary translation that keeps the filler domain free of the schedule type. The era
// defaults from the channel's PROGRAM scope era when the filler selection sets no era of
// its own (a "90s action" channel gets 90s ads for free); a Range is a from–to window,
// so we take its lower bound as the representative era for the ladder. A nil filler
// policy yields the zero Selection = the whole catalog (the additive default).
//
// Exported so the §12 pod PREVIEW derives the selection identically. If preview computed
// its own, the two would drift and the UI would confidently show pods the reconciler
// never builds — the whole failure mode preview exists to prevent.
func SelectionForChannel(ch store.Channel) filler.Selection {
	sel := SelectionFrom(ch.Policy.Filler, ch.Policy.Scope.Era)
	if ch.Policy.BreakDuration != nil {
		sel.BreakDurationMs = ch.Policy.BreakDuration.Std().Milliseconds()
	}
	return sel
}

// BreaksPerHourFor resolves a channel's commercial-break density (§10, V51f).
//
// ⚠ **One writer, because there were two identical copies and a third would have been easy.**
// `reconcile.go` and `preview.go` each carried the same `= 0; if hasFillerPool { = global }`
// three-liner — fine while there was one global value to read, and exactly the shape that grows a
// quiet disagreement the moment a per-channel override exists. Preview claiming a density
// reconcile does not apply is the drift preview exists to prevent.
//
// ⚠ **The dead-air rule wins over the operator's override, deliberately.** No pool means no
// breaks whatever the policy says: break gaps with nothing to fill them leave empty flex that
// Tunarr renders as large channel-named blocks. The override lowers density or switches breaks
// off; it cannot conjure clips.
func BreaksPerHourFor(pol schedule.ChannelPolicy, hasFillerPool bool, global int) int {
	if !hasFillerPool {
		return 0
	}
	if pol.BreaksPerHour == nil {
		return global
	}
	if n := *pol.BreaksPerHour; n > 0 {
		return n
	}
	// A present zero (or a nonsense negative) is "no breaks on this channel".
	return 0
}

// BreakDurationFor resolves the per-channel break length against the live global setting.
// Invalid zero/sub-30s values fail to the documented 5m default; disabling belongs exclusively
// to BreaksPerHour.
func BreakDurationFor(pol schedule.ChannelPolicy, global time.Duration) time.Duration {
	const fallback = 5 * time.Minute
	if pol.BreakDuration != nil {
		if d := pol.BreakDuration.Std(); d >= 30*time.Second {
			return d
		}
		return fallback
	}
	if global >= 30*time.Second {
		return global
	}
	return fallback
}

// SelectionFrom is the ONE place a filler selection becomes a domain Selection, scope era and all.
//
// ⚠ **It is exported because this rule had THREE implementations, and they did not agree.**
// This one applied the scope era; `api.fillerSelectionToDomain` (self-described as "mirroring
// channels.SelectionForChannel") did not; `app.podPreviewAdapter.PreviewDraft` applied it again.
// The API's omission was invisible because the adapter below it put the era back — two copies
// cancelling out, which is the worst kind of agreement because nothing looks wrong. It stops
// working the instant "explicitly any era" exists: a fallback keyed on `Era == 0` cannot tell an
// unset era from a chosen one, so it would overwrite the operator's answer with the channel's.
// One writer, called from every derivation, is the only version of this that stays true.
func SelectionFrom(f *schedule.FillerSelection, scopeEra *schedule.Range) filler.Selection {
	sel := filler.Selection{}
	inheritEra := true
	if f != nil {
		sel.Audience = filler.Audience(f.Audience)
		sel.Categories = f.Categories
		sel.Kinds = f.Kinds
		sel.Pinned = f.Pinned
		sel.Excluded = f.Excluded
		if f.Geography != nil {
			sel.Geography = filler.Geography{Country: f.Geography.Country, Market: f.Geography.Market}.Normalize()
		}
		if f.Era != nil {
			// ⚠ **PRESENCE is the opt-in, and that is what finally makes "any era" reachable
			// (§10, V51f).** A present range means the operator has ANSWERED the era question —
			// including answering "any" with `{0,0}`. Only absence inherits. Before this, the
			// scope default keyed off `sel.Era == 0`, so clearing the field re-inherited on the
			// very next derivation and a channel with a programming era had no way to say "draw
			// from the whole catalog". Same pattern as `AutoCurate`, for the same reason.
			inheritEra = false
			sel.Era = filler.EraRange{From: f.Era.From, To: f.Era.To}
		}
	}
	// The "seed filler era from scope.era" default, applied live rather than only stamped at
	// create — so an existing channel benefits, and a channel whose scope later changes follows.
	if inheritEra && scopeEra != nil {
		sel.Era = filler.EraRange{From: scopeEra.From, To: scopeEra.To}
	}
	return sel
}

// PodSeed derives a deterministic pod seed from the channel id (§10 seeded-
// deterministic — same channel rebuilds the same clip pool, so the filler-list
// attach is idempotent across reconciles). Exported for the same reason as PodEra:
// preview must seed from the identical value or it previews a different pod.
func PodSeed(channelID string) int64 {
	var h int64 = 1469598103934665603 // FNV-1a offset basis
	for _, b := range []byte(channelID) {
		h ^= int64(b)
		h *= 1099511628211
	}
	return h
}

// PodSeedAt derives the seed for the break STARTING AT a specific instant — what
// filler.Window has always documented ("channel + window start") and what real television
// does: consecutive breaks do not replay the same three adverts.
//
// PodSeed (channel-only) remains for the two callers that legitimately want ONE pool per
// channel rather than one pod per break:
//
//   - BuildFillerList attaches a Tunarr filler-list to the channel, and Tunarr picks from
//     that pool itself. There is no break to seed from at attach time.
//   - HasPool only asks "is there anything to play at all".
//
// Internal playout and the guide, which both resolve A SPECIFIC BREAK, use this instead. The
// two must agree — the hover card promises the clips that will actually air — and they do,
// because both derive the seed from the same (channel, break start).
func PodSeedAt(channelID string, breakStartMs int64) int64 {
	h := PodSeed(channelID)
	// Mix the start in with the same FNV-1a step, byte by byte, so nearby breaks land far
	// apart in the sequence rather than producing near-identical pods.
	for i := 0; i < 8; i++ {
		h ^= (breakStartMs >> (8 * i)) & 0xff
		h *= 1099511628211
	}
	return h
}

// ensureChannel creates or updates the Tunarr channel. On create, Tunarr assigns
// the id (Phase-0 finding 1) — EnsureChannel returns it; we must persist it.
// Handles out-of-band deletion: if we hold a TunarrID but the channel is gone,
// recreate it.
// Returns the Tunarr id, the number actually used, and whether Ensure created/recreated the
// remote row. The number may differ from the requested one when Tunarr already had a channel
// there (see below). The caller must persist both id and number in one checkpoint, or Loomarr's
// row and Tunarr disagree about what number the channel is on.
func (e *Engine) ensureChannel(ctx context.Context, localChannelID string, spec programmer.ChannelSpec) (string, int, bool, error) {
	created := spec.TunarrID == ""
	if spec.TunarrID != "" {
		actual, ok, err := e.prog.GetChannel(ctx, spec.TunarrID)
		if err != nil {
			return "", spec.Number, false, fmt.Errorf("check channel %s: %w", spec.TunarrID, err)
		}
		if !ok {
			// The channel was deleted in Tunarr out of band; recreate it.
			spec.TunarrID = ""
			created = true
		} else {
			// Preserve Tunarr's existing loop anchor across the update PUT (§9) — without
			// this the channel's lineup would jump back to its start every reconcile. This
			// reuses the GET we already make here, so it costs no extra round-trip.
			spec.StartTime = actual.StartTime
		}
	}
	// ⚠ **A CREATE onto a number Tunarr already uses can never succeed, so move rather than retry
	// it forever** (§9 V54). Tunarr reports the collision as `500` with an EMPTY BODY — there is
	// nothing in the response to match on — so the occupancy is checked BEFORE the create instead
	// of interpreting the failure afterwards.
	//
	// ⚠ It renumbers LOOMARR'S channel and never touches the occupant. §9's "channels Loomarr
	// didn't create are never touched" is the rule this design is shaped around: after a database
	// reset Loomarr genuinely cannot tell its own orphan from a stranger's channel, so it must
	// assume stranger. Moving ourselves is the only self-healing option that cannot damage
	// somebody else's channel.
	//
	// The renumber is REPORTED, not silent: the number is what a viewer tunes to, so a channel
	// that quietly moved would be a worse surprise than the failure it replaces.
	if spec.TunarrID == "" {
		moved, err := e.freeNumberFor(ctx, localChannelID, spec.Number)
		if err != nil {
			return "", spec.Number, false, err
		}
		if moved != spec.Number {
			e.log.Warn("channel number already used in Tunarr; moving this channel rather than failing",
				"channel", spec.Name, "wanted", spec.Number, "using", moved)
			if e.acts != nil {
				e.acts.Warn(ctx, "channel.renumbered", spec.Name, fmt.Sprintf(
					"%q moved to channel %d — Tunarr already had a channel on %d. Loomarr never changes a channel it didn't create.",
					spec.Name, moved, spec.Number))
			}
			spec.Number = moved
		}
	}
	id, err := e.prog.EnsureChannel(ctx, spec)
	if err != nil {
		return "", spec.Number, false, fmt.Errorf("ensure channel: %w", err)
	}
	return id, spec.Number, created, nil
}

// freeNumberFor returns `want` when neither Tunarr nor another local channel holds it, else the
// lowest number free in both stores. Best-effort for Tunarr: if its list can't be read the caller
// keeps its number and the create either succeeds or fails as before. The local list is required;
// creating remotely on a number the database already owns would manufacture an orphan before the
// unique index rejects the attachment.
func (e *Engine) freeNumberFor(ctx context.Context, localChannelID string, want int) (int, error) {
	existing, err := e.prog.ListChannels(ctx)
	if err != nil {
		e.log.Warn("couldn't read Tunarr's channels to check the number; keeping it", "number", want, "err", err)
		return want, nil //nolint:nilerr // deliberate: a list failure must not renumber a channel
	}
	used := make(map[int]bool, len(existing))
	for _, c := range existing {
		used[c.Number] = true
	}
	local, err := e.store.ListChannels(ctx)
	if err != nil {
		return want, fmt.Errorf("list local channels for number allocation: %w", err)
	}
	for _, c := range local {
		if c.ID != localChannelID {
			used[c.Number] = true
		}
	}
	if !used[want] {
		return want, nil
	}
	for n := 1; ; n++ {
		if !used[n] {
			return n, nil
		}
	}
}

// statusFor derives the channel's Loomarr-side status from its desired lineup.
//
// ⚠ AN EMPTY DECK IS NOT `live`. This used to return `live` without ever looking at
// the slots, so a channel that computed to nothing still reported healthy — and the
// operator's only symptom was an empty grid, with no hint that filtering was the
// cause. That is how a seasonal-bench bug hid: six titles on the lineup, every one
// benched `out_of_season`, `desired_json` literally `[]`, status `live`.
//
// Checked AFTER drift, because drift is the more specific claim: a channel that both
// drifted and emptied is better described by what changed under it.
//
// This does not collide with `building`, which is set once at create time
// (api/channels.go) and never by this function — reconcile only ever runs on a channel
// that has already been created, so "no slots yet" here means filtering removed them,
// not that the channel is still on its way.
func (e *Engine) statusFor(d schedule.DesiredLineup, drifted bool) schedule.ChannelStatus {
	if drifted {
		return schedule.StatusDrifted
	}
	// ⚠ Counts PROGRAMMES, not slots. `len(d.Slots) == 0` was the obvious predicate and
	// the wrong one: a deck with nothing to play still carries SlotFiller entries
	// (breaks/flex), so it is non-empty while airing no content. Caught by the test
	// below, which reported `live` against that first version.
	for _, s := range d.Slots {
		if s.Kind == schedule.SlotProgram {
			return schedule.StatusLive
		}
	}
	return schedule.StatusEmpty
}

// pokeGuide triggers a guide refresh, logging (never returning) failures (§9).
func (e *Engine) pokeGuide(ctx context.Context, channelID string) {
	if e.guide == nil {
		return
	}
	if err := e.guide.PokeGuideRefresh(ctx); err != nil {
		e.log.Warn("guide refresh poke failed (freshness degraded, reconcile ok)",
			"channel", channelID, "err", err)
	}
}

// rescanTuner asks the media server to re-read the tuner channel list so a newly
// created channel is discovered (§9), logging (never returning) failures.
func (e *Engine) rescanTuner(ctx context.Context, channelID string) {
	if e.guide == nil {
		return
	}
	if err := e.guide.RescanTuner(ctx); err != nil {
		e.log.Warn("tuner re-scan failed (new channel may not appear until periodic scan, reconcile ok)",
			"channel", channelID, "err", err)
	}
}

// staleProgramCount reports how many Keys that were real programs in the
// previously-persisted desired the library can no longer supply (§9 slot
// revalidation). A non-zero count means scheduled items genuinely vanished from
// the library since the last reconcile — the signal for StatusDrifted and the §17
// slot-substitution count. A first-ever reconcile (empty prev) can't drift.
//
// The comparison is against the freshly-computed ELIGIBLE set — every key the
// library can currently supply for this channel — NOT the aired `Slots` (§6.5).
// This is the curation-rule/rolling-window drift fix: with rules or a rolling
// window, a program legitimately leaves `Slots` because the active rule narrowed
// scope, a seasonal bench removed it, or the window rotated to a different slice —
// none of which is drift. A key is stale ONLY when it is absent from `eligible`,
// i.e. the library actually lost the title. Comparing against `Slots` (as an
// earlier version did) would false-flag every daypart/window rotation as
// StatusDrifted. A nil `eligible` (policy-free path never populates it) falls back
// to treating any prev program as still-eligible (no drift) — the whole-run,
// no-window case where prev ⊆ next holds anyway.
func staleProgramCount(prev []schedule.Slot, eligible map[provision.Key]bool) int {
	n := 0
	for _, s := range prev {
		if s.IsProgram() && s.Key != "" && eligible != nil && !eligible[s.Key] {
			n++
		}
	}
	return n
}

// lineupDiffers reports whether the desired slots differ from Tunarr's actual
// programming in a way that requires a push. Comparison is on the pushable shape
// (kind + library item + duration) since Tunarr can't round-trip our Key
// (lineup.go itemToSlot). A length change or any positional difference triggers a
// push — this is the minimal-diff decision (§9): equal ⇒ no Tunarr write.
func lineupDiffers(desired, actual []schedule.Slot) bool {
	if len(desired) != len(actual) {
		return true
	}
	for i := range desired {
		if !pushEqual(desired[i], actual[i]) {
			return true
		}
	}
	return false
}

// pushEqual compares two slots by what actually reaches Tunarr. A desired
// program/filler-with-item is a "content" item keyed by LibraryItemID; everything
// else is flex. So two slots are push-equal iff they render to the same Tunarr
// item type + id.
func pushEqual(want, got schedule.Slot) bool {
	wType, wID := pushShape(want)
	gType, gID := pushShape(got)
	return wType == gType && wID == gID
}

// pushShape returns the (tunarr-type, item-id) a slot renders to, matching
// programmer.slotToItem's logic. Kept here (not exported from programmer) so the
// diff is expressed in domain terms. Since the §10 redesign moved filler into a
// Tunarr filler-list, filler slots are ALWAYS flex in the pushed lineup — only a
// program is content.
func pushShape(s schedule.Slot) (string, string) {
	if s.Kind == schedule.SlotProgram {
		return "content", s.LibraryItemID
	}
	return "flex", ""
}

// isNotFound reports whether err is the store's not-found sentinel.
func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
