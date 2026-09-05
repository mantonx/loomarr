package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/backendtransition"
	"github.com/loomarr/loomarr/internal/channels"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/events"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/inventory"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/media"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/prepared"
	"github.com/loomarr/loomarr/internal/scheduler"
	"github.com/loomarr/loomarr/internal/settings"
	"github.com/loomarr/loomarr/internal/setup"
	"github.com/loomarr/loomarr/internal/store"
)

const playoutGPUIdentityTimeout = 250 * time.Millisecond

type playoutBuild struct {
	observer          api.PlayoutObserver
	preparedObserver  api.PreparedObserver
	service           api.Playout
	resolverService   api.PlayoutResolver
	encodePool        *media.EncodePool
	guide             api.PlayoutGuide
	resolver          *playoutResolver
	backendController *backendtransition.Controller
	setResidentVRAM   func(func(context.Context) (float64, string))
}

type playoutDeps struct {
	rootCtx               context.Context
	store                 store.Store
	settings              resolved
	owner                 *generationLifecycle
	captureResolver       func(*playoutResolver)
	library               *library.Client
	secrets               *settings.Secrets
	readSecret            func(context.Context, settings.GeneratedSecret) (string, error)
	events                *events.Bus
	jobs                  *scheduler.Registry
	layout                filler.Layout
	channels              *channels.Engine
	liveTVConnector       *setup.LiveTVConnector
	backendView           backendtransition.CheckpointView
	resolveDesiredBackend func(context.Context) (string, error)
	appliedBackend        func(context.Context) (string, error)
	transportBackend      func(context.Context) (string, error)
	log                   *slog.Logger
	processDiagnostics    *diagnostics.ProcessManager
	metrics               *metrics.Recorder
}

func buildPlayout(deps playoutDeps) (playoutBuild, error) {
	rootCtx, st, set := deps.rootCtx, deps.store, deps.settings
	owner, capturePlayoutResolver := deps.owner, deps.captureResolver
	libraryClient, readGeneratedSecret := deps.library, deps.readSecret
	lib, secrets := libraryClient, deps.secrets
	eventBus, jobReg, fillerLayout := deps.events, deps.jobs, deps.layout
	channelEngine, liveTVConnector := deps.channels, deps.liveTVConnector
	backendView, resolveDesiredBackend, log := deps.backendView, deps.resolveDesiredBackend, deps.log
	appliedBackendContext, transportBackendContext := deps.appliedBackend, deps.transportBackend
	var playoutObserver api.PlayoutObserver
	var preparedObserver api.PreparedObserver
	var playoutSvc api.Playout
	var playoutResolverSvc api.PlayoutResolver
	var hwEncodeSlots func(context.Context) int
	var encodePool *media.EncodePool
	var playoutGuideSvc api.PlayoutGuide
	var playoutRes *playoutResolver
	var backendController *backendtransition.Controller
	var residentVRAM func(context.Context) (float64, string)
	var preparedBlockSource playout.BlockSource
	var preparedMPEGTSReady func(context.Context, string, playout.EncodePlan) bool
	// Internal playout (§9.1): Loomarr serves its own channels. Wired here because this is
	// where BOTH halves already exist — the engine that answers "what airs when" and the
	// library client that resolves an item to a streamable URL.
	//
	// ⚠ **The manager is built FIRST, and the resolver second.** The profile depends on
	// how many channels are encoding (the load-aware ladder), so the resolver needs
	// `playoutMgr.ActiveCount`.
	//
	// This used to read "the cycle is broken with a func … assigned after the manager
	// exists", and there was no cycle: the manager never references the resolver. The
	// resolver was simply constructed 45 lines too early, and the field was back-patched
	// to compensate. Since `activeChannels` is called UNGUARDED, forgetting that patch
	// was a nil-func panic when a viewer tuned in — and nothing tested it. Building in
	// dependency order puts the field in the literal, where an omission is visible.
	// Nil-guarded like every other secrets read in this file: the parent's playlist URL is
	// built at SPAWN time, so an unguarded read here would panic when a viewer tunes in
	// rather than at boot — the worst place to find out.
	playoutTokenFn := func() string {
		if secrets == nil {
			return ""
		}
		token, err := readGeneratedSecret(context.Background(), settings.SecretPlayout)
		if err != nil {
			return "" // fail child URL generation closed while durable auth is unavailable.
		}
		return token
	}
	// residentVRAM (declared at function scope above) is the late-bound hook to "how much GPU VRAM
	// a resident LLM holds right now" (§9.1 V49) — the real getter is assigned far below, after the
	// LLM wiring. The budget closure reads it through that pointer; nil ⇒ assume no contention.
	//
	// playoutBudget is the DYNAMIC admission budget: concurrent VIDEO transcodes this box can
	// sustain right now (§9.1 V49). Composed from three live sources, re-read on every admission:
	//   1. MEASURED capacity — what Detect's lazy encoder trial found this box sustains
	//      (playoutRes.maxChannels). The source of truth for "how many encodes fit"; until the first
	//      trial completes, EffectiveCapacity deliberately permits one conservative transcode.
	//   2. OPERATOR SAFETY CAP — playout.max_channels, applied as a HARD CAP (min): an operator may
	//      only LOWER below the measurement (a safety throttle), never claim more than the hardware
	//      proved. 0/unset ⇒ no cap, use the measurement.
	//   3. VRAM SHADING — a resident LLM steals VRAM each hardware encode needs for its device
	//      context; the original black screen was an encoder that could not allocate under a
	//      resident model. So when a model is resident, shade the budget down by the encodes that
	//      VRAM can no longer host (~1 hardware encode per few GiB held). Reactive to the model
	//      loading/unloading, so headroom grows back when it evicts.
	playoutBudget := func() int {
		measured := 0
		if playoutRes != nil {
			measured = int(playoutRes.maxChannels.Load()) // published once by the lazy Detect trial
		}
		residentGiB := 0.0
		if residentVRAM != nil {
			residentGiB, _ = residentVRAM(rootCtx)
		}
		return playout.EffectiveCapacity(measured, set.intv("playout.max_channels"), residentGiB)
	}
	playoutMgr := playout.NewManager(
		playoutSpawner(set.str("playout.ffmpeg_path"),
			func() string { return set.str("server.public_url") },
			playoutTokenFn, log, deps.processDiagnostics,
			func() playout.BlockSource { return preparedBlockSource }),
		playoutBudget,
		playout.DefaultGrace,
		log,
	).WithObserver(deps.metrics).WithCostEstimator(func(
		ctx context.Context, channelID string, plan playout.EncodePlan,
	) int {
		if preparedMPEGTSReady != nil && preparedMPEGTSReady(ctx, channelID, plan) {
			return 0
		}
		return plan.EstimatedCost()
	})
	// A committed internal Desired-cycle change must retire the encoder reading the
	// previous cycle. The next tune starts from the new wall-clock position; peer
	// Postgres replicas receive the same cutover through durable invalidations.
	channelEngine.WithScheduleInvalidator(playoutMgr)
	playoutRes = &playoutResolver{
		engine: channelEngine, lib: lib, now: time.Now,
		metrics:       deps.metrics,
		detectContext: rootCtx,
		// The store, narrowed to GetTitle — the grid's provenance line reads acquisition
		// state and must not be able to change it.
		titles: st,
		// Same store, narrowed to the one write the resolver legitimately makes: counting
		// a filler clip as having aired (V28).
		clipPlays: st,
		// Airing history (§5, programming-design §3.1): the same store, narrowed to the one
		// write. Recording what actually aired is what lets placement prefer titles that
		// have NOT been on recently — the memory the scheduler previously lacked.
		airings: st,
		// The arranged-cycle cache and the channel read it fingerprints (cyclecache.go): the
		// guide re-arranges every channel on every poll, which profiled as 53% of the
		// request's CPU. GUIDE PATHS ONLY — AiringNow stays on the live computation, so a
		// cache bug degrades a grid rather than a broadcast.
		channels: st,
		// The store, narrowed to the single derived-column write ComputeChannelCodec makes
		// (§9.1 V50): persist the majority broadcast codec measured from the channel's content.
		codecs:   st,
		cycles:   newCycleCache(time.Now),
		tier:     func() string { return set.str("playout.quality_tier") },
		encoder:  func() string { return set.str("playout.encoder") },
		capacity: playoutBudget,
		// fillerDir belongs to the immutable generation layout. Changing storage roots
		// is applied only after the generation drains and rebuilds (§10); `pods` is
		// assigned after the pod adapter is built further down.
		fillerDir: fillerLayout.ClipDir(),
		// The capability probe runs lazily on the first program that needs it, when
		// playout.encoder is unset — so a box with a working GPU uses it instead of
		// silently falling back to software.
		ffmpegPath: func() string { return set.str("playout.ffmpeg_path") },
		capabilityRoot: func() string {
			return set.str("playout.prepared_dir")
		},
		// GPU name for the encoder chooser's vendor-native hint (Detect). Read via the LLM
		// package's thin nvidia-smi wrapper — the same GPU signal the rest of the app probes —
		// so playout picks NVENC on an NVIDIA card rather than young cross-vendor Vulkan. Called
		// once, lazily, inside the memoised capability probe; "" (unknown GPU) is a fine default.
		// Bound the external identity command because this cheap check can run on the first tune.
		gpuName: func() string {
			ctx, cancel := context.WithTimeout(rootCtx, playoutGPUIdentityTimeout)
			defer cancel()
			return llm.GPUName(ctx)
		},
		// Preferred audio language (§9.1), read live so a Settings change applies to the
		// next programme rather than the next restart. The prober derives ffprobe from the
		// ffmpeg path — the two ship together, so an operator who moved one moved both.
		audioLanguage:      func() string { return set.str("playout.audio_language") },
		inventory:          inventory.New(st),
		probeSource:        playout.FFprobeSourceNextTo(set.str("playout.ffmpeg_path"), deps.processDiagnostics),
		probeTracks:        playout.FFprobeTracksNextTo(set.str("playout.ffmpeg_path"), deps.processDiagnostics),
		probeFormat:        playout.FFprobeFormatNextTo(set.str("playout.ffmpeg_path"), deps.processDiagnostics),
		processDiagnostics: deps.processDiagnostics,
		// Live read of `library.path_map` (§15, V47), parsed each call so a mapping edit
		// applies without a restart — the same hot-apply posture as audioLanguage.
		pathMap: func() library.PathMap { return library.ParsePathMap(set.str("library.path_map")) },
		log:     log,
		// ⚠ Set HERE, in the literal, rather than back-patched after the manager exists.
		// It is called UNGUARDED (playout.Resolve → r.activeChannels()), so a missing
		// assignment is a nil-func panic on the quality-ladder path — i.e. when a viewer
		// tunes in, which is the worst place to discover it. Verified: dropping the old
		// back-patch broke no test.
		//
		// There was never a construction cycle to break: the manager does not reference
		// the resolver, so the resolver simply had to be built AFTER it.
		activeChannels: playoutMgr.ActiveCount,
	}
	// A channel starting or stopping is a STRUCTURAL change the dashboard should see
	// immediately, so it rides the SSE bus (§8: the frame is the latency path, GET
	// /v1/playout/sessions is truth). Deliberately NOT fired per ffmpeg progress sample —
	// those arrive ~1/second per stream, and republishing each would push a handful of
	// frames per second at every open browser for numbers that move by fractions.
	//
	// The payload is the CHANNEL COUNT, not the full snapshot: this layer holds the bus
	// but not the API's telemetry shape, and a frame that says "something changed" is
	// enough to make the dashboard re-read the endpoint that owns the shape.
	playoutMgr.OnChange(func() {
		eventBus.Publish(events.Event{
			Type:    "playout",
			Payload: api.PlayoutEvent{Active: playoutMgr.ActiveCount()},
		})
	})
	playoutObserver = playoutMgr

	// The in-app HLS repackager shares the session manager's encoder (§9.1 Watch, V46): it
	// attaches to a channel like any other viewer and stream-copies the bytes into HLS. A
	// failure to create its scratch root is not fatal — the media-server streams work without
	// it — so log and leave the /playout/hls routes reporting "not running" rather than
	// refusing to boot.
	var liveHLS *playout.HLSManager
	if hlsMgr, herr := playout.NewHLSManager(
		playoutMgr, set.str("playout.ffmpeg_path"), set.str("playout.hls_dir"),
		playout.DefaultGrace, log, deps.processDiagnostics,
	); herr != nil {
		log.Warn("internal playout: in-app HLS unavailable — browser playback disabled",
			"err", herr)
	} else {
		liveHLS = hlsMgr
		owner.addStop(func(context.Context) error {
			hlsMgr.Stop()
			return nil
		})
	}
	playoutResolverSvc = playoutRes

	// One-time broadcast-codec backfill (§9.1 V50). The migration defaults every existing
	// channel to h264 — a data migration runs in the store with no library access, so it
	// cannot probe. Channels bound AFTER this upgrade get their codec at bind; the ones that
	// predate it would otherwise stay h264 (and needlessly transcode an HEVC library down)
	// until their next re-curation. This pass recomputes each once, off the boot path.
	//
	// Async and best-effort: channels still play (as h264) while it runs, so a slow or
	// failing probe delays convergence, never startup. Idempotent — ComputeChannelCodec
	// writes the measured majority every time — so it needs no "already backfilled" marker
	// and re-running on a later boot is harmless bounded work.
	if st != nil {
		owner.goRun(func(ctx context.Context) {
			chans, lerr := st.ListChannels(ctx)
			if lerr != nil {
				log.Warn("playout: broadcast-codec backfill skipped (channel list failed)", "err", lerr)
				return
			}
			for _, ch := range chans {
				if ctx.Err() != nil {
					return // shutting down mid-backfill
				}
				if _, cerr := playoutRes.ComputeChannelCodec(ctx, ch.ID); cerr != nil {
					log.Debug("playout: broadcast-codec backfill for a channel failed",
						"channel", ch.ID, "err", cerr)
				}
			}
			log.Info("playout: broadcast-codec backfill complete", "channels", len(chans))
		})
	}

	// Wrap the resolver's capacity so the chosen HW-encode slot count is logged once, when the
	// admission gate first reads it — the operator-facing counterpart to "encoder probed".
	hwEncodeSlots = func(ctx context.Context) int {
		n := playoutRes.HWEncodeSlots(ctx)
		log.Info("playout: hardware encode admission", "hw_slots", n)
		return n
	}
	encodePool = media.NewEncodePool(func() int {
		if playout.Encoder(set.str("playout.encoder")) == playout.EncoderSoftware {
			return 0 // an explicit software choice must not start hardware preparation.
		}
		return hwEncodeSlots(rootCtx)
	})

	// Prepared playout is persistent control-plane work feeding the SAME Origin as the live
	// fallback. Construction may fail on an unwritable volume without taking live TV down; the
	// task remains visible with the exact reason instead of silently disappearing.
	var preparedOrigin *playout.PreparedOrigin
	preparedLibrary, preparedErr := prepared.NewLibrary(set.str("playout.prepared_dir"))
	if preparedErr != nil {
		reason := "the prepared media directory is unavailable: " + preparedErr.Error()
		log.Warn("playout: prepared media unavailable — live fallback remains active", "err", preparedErr)
		planner := prepared.NewPlanner(prepared.PlannerDependencies{
			Pool: encodePool, Now: time.Now, Log: log, UnavailableReason: reason,
		})
		preparedObserver = planner
		jobReg.Add(preparedPlayoutJob(planner, reason))
	} else {
		readiness, readinessErr := prepared.OpenReadiness(preparedLibrary)
		if readinessErr != nil {
			log.Warn("playout: prepared readiness index unavailable — live fallback remains active", "err", readinessErr)
		}
		packager := prepared.NewFFmpegPackager(
			set.str("playout.ffmpeg_path"),
			func(contract prepared.RenditionContract) (prepared.VideoPlan, error) {
				encoder := playout.Encoder(set.str("playout.encoder"))
				if encoder == "" {
					encoder = playoutRes.detectedEncoder(rootCtx)
				}
				return playout.PreparedVideoArgs(encoder, contract)
			},
		).WithDiagnostics(deps.processDiagnostics)
		preparer := prepared.NewPreparer(prepared.PreparerDependencies{
			Library: preparedLibrary, Packager: packager, Access: playoutRes,
		})
		preparedRuntime := newPreparedRuntimeResolver(preparedRuntimeDependencies{
			Channels: st, Timeline: playoutRes, Sources: playoutRes, Lookup: preparer,
			Now: time.Now, Readiness: readiness,
			PathMap: func() library.PathMap { return library.ParsePathMap(set.str("library.path_map")) },
			Policy: func() string {
				authority := ""
				if origin, err := lib.InventoryOrigin("prepared-policy"); err == nil {
					authority = string(origin.Authority)
				}
				return preparedSourcePolicy(
					set.str("playout.quality_tier"),
					set.str("playout.audio_language"),
					set.str("library.path_map"),
					authority,
				)
			},
			GlobalBackendContext:    appliedBackendContext,
			TransportBackendContext: transportBackendContext,
			Rendition: func() prepared.RenditionContract {
				return playout.CanonicalPreparedRendition(
					playout.TierFor(set.str("playout.quality_tier")),
				)
			},
		})
		planner := prepared.NewPlanner(prepared.PlannerDependencies{
			Resolver: preparedRuntime, Preparation: preparer, Pool: encodePool,
			Retainer: preparedLibrary,
			BudgetBytes: func() int64 {
				return preparedBudgetBytes(set.intv("playout.prepared_budget_gb"))
			},
			Now: time.Now, Log: log,
		})
		preparedObserver = planner
		jobReg.Add(preparedPlayoutJob(planner, ""))
		preparedOrigin = playout.NewPreparedOrigin(preparedLibrary, preparedRuntime)
		rawPreparedBlockSource := preparedOrigin.MPEGTSBlockSource(
			set.str("playout.ffmpeg_path"), log, deps.processDiagnostics,
		)
		preparedBlockSource = func(
			ctx context.Context, channelID string, plan playout.EncodePlan,
		) (playout.Block, error) {
			block, err := rawPreparedBlockSource(ctx, channelID, plan)
			if err == nil && block.Content != nil && !playoutMgr.AdmitProgram(channelID, plan, false) {
				_ = block.Content.Close()
				return playout.Block{}, playout.ErrAtCapacity
			}
			return block, err
		}
		preparedMPEGTSReady = func(ctx context.Context, channelID string, plan playout.EncodePlan) bool {
			ready, err := preparedOrigin.MPEGTSReady(ctx, channelID, plan)
			return err == nil && ready
		}
	}
	var lifecycleGate *playoutAdmissionGate
	// Every transport hop uses one durable eligibility decision, including SQLite's raw
	// playlist/program chain. Postgres additionally closes the process-wide listener gate
	// whenever notification continuity cannot be proved.
	backendView = backendtransition.NewDurableView(st)
	durablePlayoutEligibility := func(ctx context.Context, channelID string) (bool, error) {
		return durableInternalTransportPlayable(ctx, st, backendView, channelID)
	}
	if store.DialectOf(st) == store.DialectPostgres {
		lifecycleGate = &playoutAdmissionGate{}
	}
	origin := playout.NewOrigin(playout.OriginDependencies{
		Prepared: preparedOrigin, LiveSessions: playoutMgr, LiveHLS: liveHLS,
		Available: func() bool {
			return lifecycleGate == nil || lifecycleGate.Available()
		},
		Eligible: durablePlayoutEligibility,
		Observer: deps.metrics,
	})
	owner.addQuiesce(func(context.Context) error {
		origin.Quiesce()
		return nil
	})
	playoutSvc = origin

	// The durable backend checkpoint is initialized synchronously before any request can
	// observe its runtime gates. Initialize performs store I/O only; fleet and media-server
	// work is retried by settings writes and channel maintenance.
	backendURLs := func(ctx context.Context, target string) (setup.LiveTVURLs, error) {
		tok, err := readGeneratedSecret(ctx, settings.SecretPlayout)
		if err != nil {
			return setup.LiveTVURLs{}, fmt.Errorf("read playout token for backend publication: %w", err)
		}
		return setup.LiveTVURLsFor(
			target, set.str("tunarr.url"), set.str("server.public_url"), tok,
		), nil
	}
	builtBackendController, err := buildBackendTransition(rootCtx, backendTransitionDependencies{
		store: st, fleet: channelEngine,
		publisher: &backendPublisher{connector: liveTVConnector, urls: backendURLs},
		cutover:   inheritedInternalCutover{channels: st, playout: playoutSvc},
		desired:   resolveDesiredBackend,
	})
	if err != nil {
		return playoutBuild{}, err
	}
	backendController = builtBackendController
	if lifecycleGate != nil {
		lifecycle := &postgresPlayoutLifecycle{
			store: st, checkpoint: backendView, origin: origin, gate: lifecycleGate, log: log,
		}
		if err := lifecycle.StartTracked(rootCtx, owner); err != nil {
			return playoutBuild{}, fmt.Errorf("start postgres playout lifecycle: %w", err)
		}
	}
	// The ladder inputs (tier/encoder/capacity/activeChannels) are called UNGUARDED by
	// Profile, so leaving one unset is a panic when a viewer tunes in. `Profile` is
	// invoked by the spawner rather than over HTTP. Build captures the concrete resolver
	// on the returned generation, which lets package tests assert the real wiring without
	// mutable package state crossing concurrent builds.
	capturePlayoutResolver(playoutRes)
	playoutGuideSvc = playoutRes
	// A live encoder never exits on its own (playout/process.go), so shutdown MUST tear
	// them down explicitly or they outlive the process that started them.
	owner.addStop(func(context.Context) error {
		playoutMgr.Stop()
		return nil
	})
	log.Info("internal playout registered",
		"ffmpeg", set.str("playout.ffmpeg_path"), "max_channels_cap", set.intv("playout.max_channels"))

	// server.public_url is the base a media client dials for HLS segments. Internal playout
	// is the default backend, and an unset public URL makes channels appear in the guide but
	// fail at tune time (playout.go returns 503). The first-run wizard requires this (#387),
	// but an env-only, restored-DB, or API-driven install bypasses the wizard entirely and
	// otherwise gets no signal until a viewer hits the dead channel. Warn at boot so the
	// operator sees it in the logs, not in a support ticket. (§9.1; beta-readiness D-4.)
	if internalPlayoutNeedsPublicURL(set.str("server.public_url")) {
		log.Warn("internal playout: server.public_url is unset — channels will appear in the guide but fail at tune time; set SERVER_PUBLIC_URL (or server.public_url) to this instance's reachable base URL")
	}

	return playoutBuild{
		observer: playoutObserver, preparedObserver: preparedObserver, service: playoutSvc,
		resolverService: playoutResolverSvc, encodePool: encodePool, guide: playoutGuideSvc,
		resolver: playoutRes, backendController: backendController,
		setResidentVRAM: func(probe func(context.Context) (float64, string)) { residentVRAM = probe },
	}, nil
}
