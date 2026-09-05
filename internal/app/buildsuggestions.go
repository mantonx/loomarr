package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/activity"
	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/buildinfo"
	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/channels"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/events"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/httpx"
	"github.com/loomarr/loomarr/internal/images"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/proposalworkflow"
	"github.com/loomarr/loomarr/internal/recurate"
	"github.com/loomarr/loomarr/internal/reference"
	"github.com/loomarr/loomarr/internal/scheduler"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/suggest"
	"github.com/loomarr/loomarr/internal/tmdb"
)

type suggestionBuild struct {
	suggest         api.SuggestService
	workflow        api.ProposalWorkflow
	durableWorkflow *proposalworkflow.Workflow
	search          api.SearchService
	collections     api.CollectionService
	systemLLM       api.SystemLLMService
	icons           api.IconService
	images          *images.Service
	imageFetcher    *images.Fetcher
	timelineThumbs  api.TimelineThumbResolver
}

func buildSuggestions(
	rootCtx context.Context,
	st store.Store,
	set resolved,
	overrides Overrides,
	eventBus *events.Bus,
	emitter *eventEmitter,
	jobs *scheduler.Registry,
	fillerLayout filler.Layout,
	activityRecorder *activity.Recorder,
	log *slog.Logger,
	libraryClient *library.Client,
	tmdbClient *tmdb.Client,
	channelService api.ChannelService,
	approver *suggest.Approver,
	owner *generationLifecycle,
	metricRecorder *metrics.Recorder,
) (suggestionBuild, error) {
	var result suggestionBuild
	if st == nil {
		overrides.Startup.Complete(diagnostics.StartupCheckImageWorker, diagnostics.StartupSkipped,
			"database unavailable", "/settings/system/diagnostics", "")
		return result, nil
	}

	result.durableWorkflow = proposalworkflow.New(st, newID, time.Now)
	result.workflow = result.durableWorkflow

	var err error
	result.images, err = newImageService(
		st, set, overrides.ImageWorkerExecutable, buildinfo.Get().Version, metricRecorder,
	)
	if err != nil {
		overrides.Startup.Complete(diagnostics.StartupCheckImageWorker, diagnostics.StartupFailed,
			"required image worker certification failed", "/settings/system/diagnostics", "")
		return suggestionBuild{}, err
	}
	overrides.Startup.Complete(diagnostics.StartupCheckImageWorker, diagnostics.StartupPassed,
		"required worker certified", "", "")
	result.imageFetcher = registerImageJobs(
		rootCtx, jobs, result.images, imageStore{st}, fillerLayout, set, activityRecorder, log,
	)
	result.timelineThumbs = timelineThumbResolver{
		tmdb: tmdbClient, images: result.images, fetch: result.imageFetcher,
	}
	if engine, ok := channelService.(*channels.Engine); ok {
		engine.WithFranchises(tmdbFranchises{tmdb: tmdbClient})
	}

	catalogService := catalog.New(libraryClient, tmdbClient).WithPresenceSource(func() catalog.LibraryPresence {
		return libraryPresence{lib: libraryClient.Snapshot()}
	})
	result.search = searchAdapter{catalogService}
	result.collections = libraryCollections{lib: libraryClient}
	result.icons = iconAdapter{
		store: st, tmdb: tmdbClient, images: result.images, fetch: result.imageFetcher, log: log,
	}

	provider, systemLLM := buildLLM(rootCtx, set, st, eventBus, log, metricRecorder, owner)
	if overrides.LLM != nil {
		provider = overrides.LLM
	}
	suggester := suggest.New(provider, catalogService, tmdbClient, set.intv("suggest.max_acquisitions"))
	suggester.WithRatings(tmdbClient).
		WithFeedback(discoveryFeedbackSource{store: st}).
		WithReferences(reference.NewWeb(httpx.NewPublicNamedObserved("reference", httpx.TimeoutReference, metricRecorder)))
	service := suggest.NewService(st, suggester, suggest.Config{
		Workers: set.intv("job.workers"), Timeout: set.dur("job.timeout"), CacheTTL: 24 * time.Hour,
	}, newID, time.Now, log).
		WithProgressEmitter(emitter).
		WithProposalNotifier(emitter).
		WithDurableWorkflow(result.durableWorkflow).
		WithQualityRecorder(st)
	service = service.WithAutoApprove(suggest.NewAutoApprover(
		st, approver, func(context.Context) int { return set.intv("suggest.max_acquisitions") }, log,
	))
	service = service.WithAutoCurate(recurate.NewCurator(st, approver, recurateThresholds{set}, log))
	jobs.Add(recurate.NewRunner(st, service, log).WithAdjacency(catalogService).Job())

	result.suggest = service
	result.systemLLM = systemLLM
	owner.goRun(func(ctx context.Context) { service.Run(ctx) })
	log.Info("suggester started", "provider", provider.Name(), "workers", set.intv("job.workers"),
		"tmdb_configured", set.str("tmdb.api_key") != "")
	return result, nil
}
