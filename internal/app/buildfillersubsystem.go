package app

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/channels"
	"github.com/loomarr/loomarr/internal/clipfetch"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/events"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerdecision"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/programmer"
	"github.com/loomarr/loomarr/internal/scheduler"
	"github.com/loomarr/loomarr/internal/store"
)

type fillerBuild struct {
	service   api.FillerService
	decisions *fillerdecision.Service
	rights    *filler.FillerRightsRegistry
	preview   api.PodPreviewer
	taxonomy  api.TaxonomyEditor
}

func buildFillerSubsystem(
	st store.Store,
	set resolved,
	layout filler.Layout,
	log *slog.Logger,
	libraryClient *library.Client,
	eventBus *events.Bus,
	emitter *eventEmitter,
	jobs *scheduler.Registry,
	playoutResolver *playoutResolver,
	channelService api.ChannelService,
	processDiagnostics *diagnostics.ProcessManager,
	metricRecorder *metrics.Recorder,
	owner *generationLifecycle,
) fillerBuild {
	var result fillerBuild
	if st == nil {
		return result
	}
	rightsRegistry, err := filler.NewFillerRightsRegistry(st)
	if err != nil {
		log.Error("could not construct filler rights registry", "err", err)
	} else {
		result.rights = rightsRegistry
	}
	decisionService, err := fillerdecision.New(st)
	if err != nil {
		log.Error("could not construct filler decision service", "err", err)
	} else {
		result.decisions = decisionService
	}
	var admissionObserver filler.AdmissionObserver
	if decisionService != nil {
		admissionObserver, err = fillerdecision.NewShadow(decisionService, filleradmission.Policy{
			Version:         "production-shadow-v1",
			TaxonomyVersion: "production-shadow-no-product-taxonomy-v1",
			AllowedContentRoles: []string{
				filleradmission.RoleCommercial, filleradmission.RoleBumper,
				filleradmission.RolePSA, filleradmission.RoleStationID,
				filleradmission.RoleTrailer, filleradmission.RoleInterstitial,
			},
		}, "production-pipeline-evidence-v1")
		if err != nil {
			log.Error("could not construct filler admission shadow", "err", err)
		}
	}
	// Background acquisition workers are process-owned in the single-replica beta. Any queued or
	// running rows visible before this process accepts requests belonged to the previous process
	// and can no longer complete; close them instead of promising work that does not exist.
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if n, err := st.RecoverInterruptedAcquisitionRuns(recoveryCtx, time.Now().UTC()); err != nil {
		log.Warn("could not recover interrupted filler acquisitions", "err", err)
	} else if n > 0 {
		log.Info("recovered interrupted filler acquisitions", "runs", n)
	}
	if n, err := st.RecoverInterruptedInteractiveOperations(recoveryCtx, time.Now().UTC()); err != nil {
		log.Warn("could not recover interrupted interactive operations", "err", err)
	} else if n > 0 {
		log.Info("recovered interrupted interactive operations", "operations", n)
	}
	if n, err := st.RecoverInterruptedSpokenSafetyRuns(recoveryCtx, time.Now().UTC()); err != nil {
		log.Warn("could not recover interrupted spoken-safety runs", "err", err)
	} else if n > 0 {
		log.Info("recovered interrupted spoken-safety runs", "runs", n)
	}
	recoveryCancel()

	if dir := layout.ClipDir(); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			log.Warn("could not create the filler drop-folder; the catalog scan will report it",
				"dir", dir, "err", err)
		}
		if watch := layout.WatchDir(); watch != "" {
			if err := os.MkdirAll(watch, 0o750); err != nil {
				log.Warn("could not create the filler watch folder; incoming clips cannot be accepted",
					"dir", watch, "err", err)
			}
		}
	}
	artifactRecoveryCtx, artifactRecoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if recovered, err := clipfetch.RecoverAcquisitionArtifacts(artifactRecoveryCtx, layout.WatchDir(), layout.ClipDir(), st, time.Now); err != nil {
		log.Warn("could not recover filler acquisition artifacts", "err", err)
	} else if recovered.Published > 0 || recovered.Repair > 0 || recovered.Pending > 0 {
		log.Info("recovered filler acquisition artifacts",
			"published", recovered.Published, "repair", recovered.Repair, "pending", recovered.Pending)
	}
	artifactRecoveryCancel()

	fillerProgrammer := programmer.NewDynamicObserved(set.tunarrConfig(), metricRecorder)
	wake := &fillerChannelWake{st: st, channels: channelService, log: log}
	result.taxonomy = taxonomyEditor{store: st, wake: wake}
	syncer := buildSyncer(st, set, layout, log, fillerProgrammer, libraryClient)
	taggerProvider, tagger := buildTagger(st, set, layout, log, wake, metricRecorder)
	fetcher := buildFetcher(set, layout, log, st)
	splitter := buildSplitter(st, set, layout, log, wake, metricRecorder)
	adapter := fillerServiceAdapter{
		syncer: syncer, tagger: tagger, fetcher: fetcher,
		bus: eventBus, log: log, newID: newID, timeout: set.dur("ingest.timeout"),
		start: owner.startInteractiveOperation, operations: st,
		sources: st, pullPlanning: st, acquisitions: st, readiness: st, now: time.Now,
		home: func() filler.Geography {
			return filler.Geography{Country: set.str("filler.home_country"), Market: set.str("filler.home_market")}
		},
		splitter: splitter, splitClips: fillerSplitStoreAdapter{st: st, wake: wake},
	}

	pods := buildPodAdapter(st, set, log).WithMetrics(metricRecorder)
	result.preview = podPreviewAdapter{store: st, pods: pods}
	adapter.pool = result.preview.Pool
	if playoutResolver != nil {
		playoutResolver.pods = result.preview
	}
	if engine, ok := channelService.(*channels.Engine); ok {
		engine.WithPods(pods)
		log.Info("filler pod assembler wired into the scheduler")
	}

	jobs.Add(fillerSyncJob(syncer))
	log.Info("filler catalog sync registered", "dir", layout.ClipDir(),
		"every", set.dur("filler.sync_every"), "ai_tagging", set.boolv("filler.ai_tagging"))
	pipeline := buildPipeline(st, set, layout, log, emitter, splitter, taggerProvider, wake,
		processDiagnostics, admissionObserver, metricRecorder)
	jobs.Add(fillerPipelineJob(pipeline))
	adapter.pipeline = pipeline
	adapter.afterIngest = func(ctx context.Context) error {
		if _, err := syncer.Sync(ctx); err != nil {
			return err
		}
		// Enrol only. Running a whole pipeline pass here could make one completed download wait on
		// an unrelated transcode/Whisper backlog; the scheduled job remains the stage driver.
		_, err := pipeline.EnrolMissing(ctx)
		return err
	}
	jobs.Add(fillerSplitSweepJob(filler.NewSplitSweeper(
		fillerSweepStoreAdapter{st}, layout.ClipDir(),
		func() time.Duration { return set.dur("filler.split.review_window") }, time.Now, log,
	)))
	sourceEnumerator := registeredSourceEnumerator{youtube: clipfetch.NewYouTubeEnumerator(resolveTool(set.str("ingest.ytdlp_path"), "yt-dlp"))}
	adapter.sourceEnum = sourceEnumerator
	autoFetch := filler.NewFetcher(
		fetchStoreAdapter{
			st:         st,
			fetchEvery: func() time.Duration { return set.dur("filler.fetch.every") },
			home: func() filler.Geography {
				return filler.Geography{Country: set.str("filler.home_country"), Market: set.str("filler.home_market")}
			},
		},
		sourceEnumerator, adapter, layout.ClipDir(),
		filler.FetchLimits{
			MaxPerRun:       func() int { return set.intv("filler.fetch.max_per_run") },
			MaxCatalogClips: func() int { return set.intv("filler.fetch.max_catalog_clips") },
			MaxDiskGB:       func() int { return set.intv("filler.fetch.max_disk_gb") },
		}, log,
	).WithEnabled(func() bool { return set.dur("filler.fetch.every") > 0 })
	adapter.autoFetch = autoFetch
	result.service = adapter
	jobs.Add(fillerFetchJob(autoFetch))
	log.Info("filler auto-fetch registered", "every", set.dur("filler.fetch.every"),
		"max_per_run", set.intv("filler.fetch.max_per_run"))
	return result
}
