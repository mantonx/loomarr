package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/activity"
	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/backendtransition"
	"github.com/loomarr/loomarr/internal/binder"
	"github.com/loomarr/loomarr/internal/channels"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/events"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/media"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/programmer"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/reconcile"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/scheduler"
	"github.com/loomarr/loomarr/internal/settings"
	"github.com/loomarr/loomarr/internal/setup"
	"github.com/loomarr/loomarr/internal/store"
)

type channelBuild struct {
	channelService         api.ChannelService
	liveTV                 api.LiveTVService
	tunerRescanner         api.TunerRescanner
	tunarrConnector        api.TunarrConnector
	backendController      *backendtransition.Controller
	refreshBackendSettings func(context.Context) error
	desiredBackend         func(context.Context) (string, error)
	appliedBackend         func(context.Context) (string, error)
	checkpoint             func(context.Context) (backendtransition.Snapshot, error)
	playoutObserver        api.PlayoutObserver
	preparedObserver       api.PreparedObserver
	playout                api.Playout
	playoutResolverService api.PlayoutResolver
	encodePool             *media.EncodePool
	playoutGuide           api.PlayoutGuide
	playoutResolver        *playoutResolver
	setResidentVRAM        func(func(context.Context) (float64, string))
	channelNumbers         binder.NumberSource
}

func buildChannels(
	rootCtx context.Context, st store.Store, set resolved, ov Overrides,
	owner *generationLifecycle, capturePlayoutResolver func(*playoutResolver),
	libraryClient *library.Client, secrets *settings.Secrets,
	readGeneratedSecret func(context.Context, settings.GeneratedSecret) (string, error),
	eventBus *events.Bus, emitter *eventEmitter, activityRec *activity.Recorder,
	jobReg *scheduler.Registry, episodeRefresh *reconcile.EpisodeRefresh, fillerLayout filler.Layout,
	log *slog.Logger, processDiagnostics *diagnostics.ProcessManager,
	metricRecorder *metrics.Recorder,
) (channelBuild, error) {
	// Scheduler + Tunarr (§9, Phase 10): the channel reconcile engine + periodic
	// sweep, plus the Live TV wiring connector (guide-refresh poker). Wired when a
	// store, Tunarr, and the media-server library are configured.
	var channelSvc api.ChannelService
	var liveTVSvc api.LiveTVService
	var tunerRescanner api.TunerRescanner
	var tunarrConnectSvc api.TunarrConnector
	var channelEngine *channels.Engine
	var liveTVConnector *setup.LiveTVConnector
	var backendController *backendtransition.Controller
	var backendView backendtransition.CheckpointView
	// A replica may enter the transition lock after another replica saved a newer
	// target or changed who owns it. Refreshing the settings snapshot inside that lock
	// makes durable values and provenance, rather than whichever process happened to
	// enqueue first, authoritative.
	refreshBackendSettings := func(ctx context.Context) error {
		if set.svc != nil {
			if err := set.svc.Refresh(ctx); err != nil {
				return fmt.Errorf("refresh settings before backend transition: %w", err)
			}
		}
		return nil
	}
	desiredBackend := func(context.Context) (string, error) {
		return set.str("playout.backend"), nil
	}
	resolveDesiredBackend := func(ctx context.Context) (string, error) {
		if err := refreshBackendSettings(ctx); err != nil {
			return "", err
		}
		return desiredBackend(ctx)
	}
	checkpointSnapshot := func(ctx context.Context) (backendtransition.Snapshot, error) {
		if backendView == nil {
			return backendtransition.Snapshot{}, fmt.Errorf("backend transition checkpoint is unavailable")
		}
		return backendView.Snapshot(ctx)
	}
	appliedBackendContext := func(ctx context.Context) (string, error) {
		snapshot, err := checkpointSnapshot(ctx)
		return snapshot.Applied, err
	}
	reconcileBackendContext := func(ctx context.Context) (string, error) {
		snapshot, err := checkpointSnapshot(ctx)
		return snapshot.ReconcileBackend(), err
	}
	transportBackendContext := func(ctx context.Context) (string, error) {
		snapshot, err := checkpointSnapshot(ctx)
		if err != nil {
			return "", err
		}
		if snapshot.PublishedInternal {
			return backendtransition.BackendInternal, nil
		}
		return snapshot.Applied, nil
	}
	// Internal playout (§9.1). Nil until wired below, which keeps the routes reporting "not
	// running" rather than half-serving when there is no store or no media server.
	var playoutObserver api.PlayoutObserver
	// Prepared readiness is observed through the planner itself; the API only snapshots it.
	var preparedObserver api.PreparedObserver
	// The in-app HLS repackager (§9.1 Watch, V46). Built beside the session manager below; nil
	// until then so the /playout/hls routes report "not running" on an unwired install.
	var playoutSvc api.Playout
	var playoutResolverSvc api.PlayoutResolver
	// One host-wide pool arbitrates the measured hardware slots between live playout and prepared
	// media. It is created beside the resolver that owns capability detection, then handed to both
	// consumers; neither may maintain a private GPU counter.
	var encodePool *media.EncodePool
	// The XMLTV guide (§9.1, V6b). Satisfied by the SAME *playoutResolver as above — one
	// source for "what airs when", so the guide cannot advertise something the encoder does
	// not play.
	var playoutGuideSvc api.PlayoutGuide
	// Declared out here so the pod assembler can be attached further down: the resolver is
	// built alongside the channel engine, while the pod adapter needs the filler catalog that
	// is wired later. Both halves are required before a break can play a real commercial.
	var playoutRes *playoutResolver
	var setResidentVRAM func(func(context.Context) (float64, string))
	// Which channel numbers Tunarr already uses (§9 V54). Function scope for the same reason as
	// `residentVRAM` above: it is SET where the programmer is built, and READ far below where the
	// binder is assembled. Nil until assigned, which numbering treats as "Loomarr's store only".
	var chanNumbers binder.NumberSource
	if st != nil {
		lib := libraryClient
		prog := programmer.NewDynamicObserved(set.tunarrConfig(), metricRecorder)
		// Every production caller supplies an explicit URL snapshot from the durable checkpoint.
		// The connector's fixed fallback is empty so accidentally using a compatibility helper
		// fails closed instead of publishing a process-local target.
		liveTVConnector = setup.NewLiveTVConnector(
			func() library.LiveTV { return libraryClient.Snapshot() }, setup.LiveTVURLs{})
		liveTVSvc = liveTVAdapter{
			c: liveTVConnector,
			urls: func(ctx context.Context) (setup.LiveTVURLs, error) {
				if set.svc != nil {
					if err := set.svc.Refresh(ctx); err != nil {
						return setup.LiveTVURLs{}, fmt.Errorf("refresh settings before live TV status: %w", err)
					}
				}
				backend, err := appliedBackendContext(ctx)
				if err != nil {
					return setup.LiveTVURLs{}, err
				}
				tok, err := readGeneratedSecret(ctx, settings.SecretPlayout)
				if err != nil {
					return setup.LiveTVURLs{}, fmt.Errorf("read playout token for live TV status: %w", err)
				}
				return setup.LiveTVURLsFor(
					backend, set.str("tunarr.url"), set.str("server.public_url"), tok,
				), nil
			},
		}
		transportFreshness := transportTunerRescanner{
			c: liveTVConnector,
			urls: func(ctx context.Context) (setup.LiveTVURLs, error) {
				backend, err := transportBackendContext(ctx)
				if err != nil {
					return setup.LiveTVURLs{}, err
				}
				tok, err := readGeneratedSecret(ctx, settings.SecretPlayout)
				if err != nil {
					return setup.LiveTVURLs{}, fmt.Errorf("read playout token for tuner refresh: %w", err)
				}
				return setup.LiveTVURLsFor(
					backend, set.str("tunarr.url"), set.str("server.public_url"), tok,
				), nil
			},
		}
		tunerRescanner = transportFreshness

		// Wire the media server as Tunarr's media source (§6, POST /v1/setup/tunarr-connect):
		// uses the concrete Tunarr client's media-source methods (prog), or the injected
		// double in tests when it implements them. Library flavor/url/token resolved live.
		var msProg setup.MediaSourceProgrammer = prog
		if ov.Programmer != nil {
			if msp, ok := ov.Programmer.(setup.MediaSourceProgrammer); ok {
				msProg = msp
			}
		}
		tunarrConnectSvc = setup.NewMediaSourceConnector(
			func() setup.MediaSourceLibrary { return libraryClient.Snapshot() }, msProg)

		// Program duration comes from the media server (§9/§10): give the scheduler
		// a resolver so program slots carry a real runtime before the Tunarr push,
		// and an episode resolver so a series entry expands into its episodes (§9).
		avail := channels.NewStoreAvailability(rootCtx, st, lib.ItemDurationMs, episodeResolver(lib))
		avail = channels.WithEpisodeMaxAge(avail, func() time.Duration { return set.dur("episodes.max_age") })
		// Bulk duration resolution, so a cycle layout costs ONE media-server call instead of one
		// per movie (§9 N+1). ItemMetadataByID already asks for RunTimeTicks and already batches
		// by id list — the bulk answer simply was not wired to the scheduler's duration path, so
		// a 25-movie channel paid 25 sequential round trips on every cold guide request (~375ms
		// of GET /v1/guide, measured against the dev Emby).
		avail = channels.WithBulkDurations(avail, bulkDurations(lib))
		// Tests inject an in-process Tunarr double here; production uses the real
		// URL-built programmer (prog). Either way the engine is a *channels.Engine.
		pusher := programmer.Programmer(prog)
		if ov.Programmer != nil {
			pusher = ov.Programmer
		}
		// ⚠ Taken from `pusher`, NOT `prog`: tests inject an in-process Tunarr double here, and
		// numbering that consulted the real client while the reconcile used the double would
		// disagree about which numbers exist. Both must read the same Tunarr.
		chanNumbers = tunarrNumbers{pusher}
		channelEngine = channels.New(st, pusher, avail, transportFreshness, channels.Config{
			// Pending-slot policy defaults to pod-fill (§9); the interstitial-card
			// alternative is future design work; backfill is stable today
			// placement, handled inside the engine, not the placeholder kind.
			Policy:               schedule.PodFill,
			ReconcileTTL:         set.dur("channel.reconcile_every"),
			BreaksPerHour:        set.intv("filler.breaks_per_hour"),
			BreakDuration:        set.dur("filler.break_duration"),
			DefaultWindow:        set.dur("sched.window_hours"),
			ResolveReconcileTTL:  func() time.Duration { return set.dur("channel.reconcile_every") },
			ResolveBreaksPerHour: func() int { return set.intv("filler.breaks_per_hour") },
			ResolveBreakDuration: func() time.Duration { return set.dur("filler.break_duration") },
			ResolveDefaultWindow: func() time.Duration { return set.dur("sched.window_hours") },
			// Backend selection is durable and per-channel-aware inside the engine: this closure
			// supplies the durable in-progress target when one exists, otherwise the applied
			// global fallback, while schedule.PlaysInternally applies a channel's policy override.
			// This keeps ordinary reconcile aligned with the fleet barrier during a transition.
			ResolvePlayoutBackendContext: reconcileBackendContext,
		}, time.Now, log).WithMetrics(metricRecorder).WithQualityRecorder(quality.NewSchedulingRecorder(st, log))
		// Heal an entry that reached the scheduler unrated once its title is in the
		// library (§389 amendment): without this a fail-closed audience ceiling drops
		// it and the channel plays nothing (§9). Uses the same library client the
		// availability resolver does.
		channelEngine.WithRatings(libraryRatings{lib: lib})
		// Stamp media-server collection membership so scope.collections enforces with no
		// library I/O on the scheduling path (programming-design §2.2). Shares the library
		// client; the reverse index is built once and cached behind a TTL.
		channelEngine.WithBoxSets(&libraryBoxSets{lib: lib, ttl: 15 * time.Minute})
		// Emit a `channel` SSE frame after each reconcile so the UI updates live — the
		// "no manual rebuild" model (§9). The emitter already fans to the event bus.
		channelEngine.WithNotifier(emitter)
		// A reconcile that has to MOVE a channel because Tunarr already occupies its number is an
		// operator-facing fact that must outlive a log line (§9 V54) — the number is what a viewer
		// tunes to, and the log that recorded the original strand had already scrolled away by the
		// time anyone asked what happened.
		if activityRec != nil {
			channelEngine.WithActivity(activityRec)
		}
		channelSvc = channelEngine

		// Now that the scheduler engine exists, give the emitter its backfill
		// handler: provisioning availability events (webhook + reconciler) fan to
		// OnAvailability, so an acquisition that lands `available` reconciles the
		// channels referencing it immediately — instead of waiting up to a full
		// sweep interval. (#10: the emitter was nil before this wire.)
		emitter.setEngine(channelEngine)

		backendView = backendtransition.NewDurableView(st)
		playoutBuilt, err := buildPlayout(playoutDeps{
			rootCtx: rootCtx, store: st, settings: set, owner: owner,
			captureResolver: capturePlayoutResolver, library: libraryClient, secrets: secrets,
			readSecret: readGeneratedSecret, events: eventBus, jobs: jobReg, layout: fillerLayout,
			channels: channelEngine, liveTVConnector: liveTVConnector, backendView: backendView,
			resolveDesiredBackend: resolveDesiredBackend, appliedBackend: appliedBackendContext,
			transportBackend: transportBackendContext, log: log,
			processDiagnostics: processDiagnostics,
			metrics:            metricRecorder,
		})
		if err != nil {
			return channelBuild{}, err
		}
		playoutObserver, preparedObserver = playoutBuilt.observer, playoutBuilt.preparedObserver
		playoutSvc, playoutResolverSvc = playoutBuilt.service, playoutBuilt.resolverService
		encodePool, playoutGuideSvc = playoutBuilt.encodePool, playoutBuilt.guide
		playoutRes, backendController = playoutBuilt.resolver, playoutBuilt.backendController
		setResidentVRAM = playoutBuilt.setResidentVRAM
		chEvery := set.dur("channel.reconcile_every")
		// The channel sweep is a scheduler job now (§18.1) — same desired-vs-actual Sweep
		// (with its own ClaimDueChannels lease), driven by the shared heartbeat. The Runner's
		// lease/batch are still constructed; only its standalone loop is gone.
		chSweep := channels.NewRunner(channelEngine, st, chEvery, 2*chEvery, 50, time.Now, log)
		jobReg.Add(channelMaintenanceJob(chSweep, episodeRefresh, func(ctx context.Context) error {
			return backendController.ApplyCurrent(ctx, resolveDesiredBackend)
		}))
		log.Info("channel scheduler registered", "tunarr", set.str("tunarr.url"), "sweep_every", chEvery)
	}
	return channelBuild{
		channelService: channelSvc, liveTV: liveTVSvc, tunerRescanner: tunerRescanner,
		tunarrConnector: tunarrConnectSvc, backendController: backendController,
		refreshBackendSettings: refreshBackendSettings, desiredBackend: desiredBackend,
		appliedBackend: appliedBackendContext, checkpoint: checkpointSnapshot,
		playoutObserver:  playoutObserver,
		preparedObserver: preparedObserver, playout: playoutSvc,
		playoutResolverService: playoutResolverSvc, encodePool: encodePool,
		playoutGuide: playoutGuideSvc, playoutResolver: playoutRes,
		setResidentVRAM: setResidentVRAM,
		channelNumbers:  chanNumbers,
	}, nil
}
