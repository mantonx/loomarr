package app

import (
	"log/slog"
	"time"

	"github.com/loomarr/loomarr/internal/activity"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/reconcile"
	"github.com/loomarr/loomarr/internal/retention"
	"github.com/loomarr/loomarr/internal/scheduler"
	"github.com/loomarr/loomarr/internal/store"
)

// buildProvisioning registers acquisition, availability, retention, and queue work and returns
// the one episode refresher consumed later by channel maintenance.
func buildProvisioning(
	st store.Store,
	set resolved,
	libraryClient *library.Client,
	emitter *eventEmitter,
	jobs *scheduler.Registry,
	activityRecorder *activity.Recorder,
	processDiagnostics *diagnostics.ProcessManager,
	log *slog.Logger,
	metricRecorder *metrics.Recorder,
) *reconcile.EpisodeRefresh {
	if st == nil {
		return nil
	}
	reconciler := reconcile.NewDynamic(st, set.requesterFor(metricRecorder), func() reconcile.LibraryLookup {
		return libraryClient.Snapshot()
	}, emitter, reconcile.Config{
		RequestTTL: set.dur("request.ttl"), DownloadingTTL: set.dur("downloading.ttl"),
	}, time.Now, log).WithQualityRecorder(quality.NewAcquisitionRecorder(st, log))
	jobs.Add(reconciler.Job())
	jobs.Add(retention.New(st, retention.Windows{
		Proposals:   func() time.Duration { return set.dur("proposals.retention") },
		Jobs:        func() time.Duration { return set.dur("jobs.retention") },
		Activity:    func() time.Duration { return set.dur("activity.retention") },
		Diagnostics: func() time.Duration { return set.dur("diagnostics.retention") },
		DiagnosticsMaxBytes: func() int64 {
			return int64(set.intv("diagnostics.max_storage_mb")) * 1024 * 1024
		},
	}, time.Now, log).WithDiagnostics(processDiagnostics).Job())

	libraryScan := reconcile.NewLibraryScan(
		st, libraryClient, emitter, set.dur("job.library_scan.lookback"), time.Now, log,
	).WithActivity(activityRecorder).WithQualityRecorder(quality.NewAcquisitionRecorder(st, log))
	jobs.AddAll(libraryScan.Jobs())

	episodeRefresh := reconcile.NewDynamicEpisodeRefresh(st, func() reconcile.EpisodeResolver {
		return episodeResolver(libraryClient.Snapshot())
	}, func() time.Duration {
		return set.dur("episodes.max_age")
	}, time.Now, log)

	if arr := set.arrRequester(metricRecorder); arr != nil {
		queuePoll := reconcile.NewQueuePoll(st, arr, emitter, set.dur("downloading.ttl"), time.Now, log)
		jobs.Add(queuePoll.Job("arr-queue-poll", "Poll Sonarr and Radarr",
			"Updates acquisition progress and detects stalled downloads from Sonarr and Radarr."))
	} else if seerr := set.seerrRequester(metricRecorder); seerr != nil {
		queuePoll := reconcile.NewQueuePoll(st, seerr, emitter, set.dur("downloading.ttl"), time.Now, log)
		jobs.Add(queuePoll.Job("seerr-queue-poll", "Poll Seerr acquisitions",
			"Updates requested titles from the coarse acquisition states reported by Seerr."))
	}
	return episodeRefresh
}
