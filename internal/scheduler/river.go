package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/robfig/cron/v3"

	"github.com/loomarr/loomarr/internal/store"
)

// River is the background-job engine (§14, §18.1): the registry's jobs become River periodic
// jobs, and River owns due-selection, leadership, retries and the durable job records.
//
// ⚠ **`scheduled_jobs` is still the read model the Tasks page renders**, and that is deliberate
// rather than leftover. River's tables are keyed by job EXECUTION — one row per attempt — while
// the page answers "what is this named task's current state?". Deriving the latter from the
// former on every page load means a group-by over a growing history to rebuild something we
// already write once per run. It also keeps `paused` (an operator preference about a task) out
// of a table that models attempts.
//
// ⚠ **Cron: NEVER `cron.ParseStandard`.** River's docs point at it and it is FIVE-field, while
// every schedule Loomarr has is six-field seconds-leading (`0 */5 * * * *`, Overseerr-shaped).
// Those values are operator-editable settings already in the database, so following the
// documented example would reject every saved schedule at boot on installs that were working
// fine. Verified in docs/engineering/FINDINGS-river-spike-2026-07-30.md.
var cronParser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// jobArgs is the River job payload. One kind per registered job name, so River's own history
// is readable ("kind: library-scan") rather than a single opaque "run" kind.
type jobArgs struct {
	Name   string `json:"name"`
	Manual bool   `json:"manual,omitempty"`
}

const legacyJobKind = "loomarr_job"

// Kind gives each named task its own River kind, making one task's history directly queryable
// without scanning executions from every other task. The empty-name kind remains registered so
// jobs queued by an older Loomarr build can drain after an upgrade.
func (a jobArgs) Kind() string {
	if a.Name == "" {
		return legacyJobKind
	}
	return legacyJobKind + ":" + a.Name
}

type jobExecutionOutput struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	DurationMs int64     `json:"durationMs"`
	Result     string    `json:"result"`
	Error      string    `json:"error,omitempty"`
}

// riverWorker executes one registered job by name and records the outcome in `scheduled_jobs`,
// preserving the exact contract the Tasks page reads: last run, last result, last error.
type riverWorker struct {
	river.WorkerDefaults[jobArgs]
	s *Scheduler
}

// Timeout is how long THIS job may run before River cancels its context.
//
// ⚠ **Without this override every job ran under River's `JobTimeoutDefault`, which is ONE
// MINUTE** — `river.Config.JobTimeout` was never set, and zero means the default. That ceiling
// was inherited from a dependency rather than chosen, and it is far below what the media jobs
// need: measured 2026-08-11, one `blackdetect`/`silencedetect` pass over a 20-minute recording
// takes **40s on its own**, before the dedup and whisper work that follows it in the same pass.
//
// ⚠ The failure it produced never said "timeout". `exec.CommandContext` SIGKILLs its child when
// the deadline fires, so the operator-visible symptom was `signal: killed` from ffmpeg and
// whisper — which reads as a corrupt file or a broken binary, and sent this session chasing
// both. A job that is out of time should say so; see `Job.Timeout` for the rest of that note.
//
// Returning 0 keeps River's default, which is right for the cheap jobs: a sweep that has not
// finished in a minute is stuck, not slow.
func (w *riverWorker) Timeout(rj *river.Job[jobArgs]) time.Duration {
	if j, ok := w.s.jobs[rj.Args.Name]; ok {
		return j.Timeout
	}
	return 0
}

func (w *riverWorker) Work(ctx context.Context, rj *river.Job[jobArgs]) error {
	j, ok := w.s.jobs[rj.Args.Name]
	if !ok {
		// A queued job for a name no longer in code (renamed, or removed in an upgrade).
		// Discard it rather than erroring: retrying can never make the name exist, and a
		// permanently-failing row would sit red on the Tasks page describing nothing.
		w.s.log.Warn("scheduler: queued job has no registered code", "job", rj.Args.Name)
		return nil
	}
	// ⚠ Both guards are load-bearing at execution time, not only at insert time. A job can be
	// paused or become disabled AFTER its row was queued — a SQLite → Postgres migration is the
	// concrete case for disabled (§18, V11) — and River would otherwise happily run work the
	// operator stopped or the backend cannot support.
	if j.Disabled() {
		return nil
	}
	if paused, err := w.s.isPaused(ctx, j.Name); err == nil && paused && !rj.Args.Manual {
		return nil
	}
	out := w.s.execute(ctx, j, rj.Args.Manual)
	// River owns the durable execution rows. Recording Loomarr's outcome here lets a failed
	// task remain a completed queue item (the next cron tick is its retry policy) while still
	// preserving the operator-visible failure in that execution's history.
	if err := river.RecordOutput(ctx, out); err != nil {
		w.s.log.Warn("scheduler: record River job output", "job", j.Name, "err", err)
	}
	return nil
}

// isPaused reads the operator's pause flag. A read error is reported as NOT paused: failing
// open means a transient database hiccup skips one run at worst, where failing closed would
// silently stop every job on the install.
func (s *Scheduler) isPaused(ctx context.Context, name string) (bool, error) {
	row, err := s.store.GetScheduledJob(ctx, name)
	if err != nil {
		return false, err
	}
	return row.Paused, nil
}

// riverDriverFor returns the River driver matching the store's backend.
//
// ⚠ SQLite support is officially EXPERIMENTAL (§14) — a stated, accepted risk, not an
// oversight. Postgres uses the database/sql driver rather than pgx because that is the pool
// shape the store already holds.
func riverDriverFor(st store.Store, db *sql.DB) (riverdriver.Driver[*sql.Tx], error) {
	switch store.DialectOf(st) {
	case store.DialectSQLite:
		return riversqlite.New(db), nil
	case store.DialectPostgres:
		return riverdatabasesql.New(db), nil
	default:
		return nil, fmt.Errorf("scheduler: no River driver for this store")
	}
}

// StartRiver builds and starts the River client for this scheduler's registry, applying
// River's own schema first.
//
// ⚠ **Migrations run PROGRAMMATICALLY via rivermigrate, never the `river migrate-up` CLI.**
// goose owns the application catalog; River owns its own tables. Two migration LIBRARIES is a
// stated cost of adopting River — two migration SYSTEMS an operator must run would not be
// shippable, and the programmatic path is what avoids it.
func (s *Scheduler) StartRiver(ctx context.Context, st store.Store, db *sql.DB, log *slog.Logger) (func(context.Context) error, error) {
	driver, err := riverDriverFor(st, db)
	if err != nil {
		return nil, err
	}
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: river migrator: %w", err)
	}
	res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: river migrate: %w", err)
	}
	if len(res.Versions) > 0 {
		log.Info("river schema migrated", "versions", len(res.Versions))
	}

	workers := river.NewWorkers()
	worker := &riverWorker{s: s}
	// Keep the old shared kind registered only to drain rows queued before named kinds shipped.
	river.AddWorker(workers, worker)
	for _, name := range s.order {
		river.AddWorkerArgs(workers, jobArgs{Name: name}, worker)
	}

	periodic, err := s.periodicJobs()
	if err != nil {
		return nil, err
	}

	client, err := river.NewClient(driver, &river.Config{
		// ⚠ MaxWorkers 1 on SQLite is not tuning, it is correctness-adjacent: the store holds
		// MaxOpenConns(1) because modernc serializes writes, so concurrent workers would spend
		// their time contending for the one connection. These are a dozen cron jobs, not a
		// throughput workload.
		//
		// ⚠ **That reasoning is about the DEFAULT queue's WIDTH, and it is not a prohibition on a
		// second producer** — see `riverQueues`. A long media job spends its time inside
		// `exec.Command` holding no connection at all, so a second worker running beside it
		// contends for nothing.
		Queues:       s.riverQueues(st),
		Workers:      workers,
		PeriodicJobs: periodic,
		Logger:       log,
		// Run-now latency. River's default fetch poll is 1 SECOND and SQLite has no
		// LISTEN/NOTIFY to short-circuit it, so a click would wait up to a second where the
		// old in-process scheduler started immediately. 100ms keeps that feeling immediate,
		// 100× above River's floor, at the cost of one query per 100ms against a local
		// database holding a dozen rows.
		//
		// ⚠ This is NOT what made Run-now slow — see TriggerRiver. Measured before blaming it:
		// with a `ScheduledAt` offset in the insert, every trigger took ~4.97s regardless of
		// this value, and lowering the poll made no difference at all.
		FetchPollInterval: 100 * time.Millisecond,
	})
	if err != nil {
		return nil, fmt.Errorf("scheduler: river client: %w", err)
	}
	completed, cancelCompleted := client.Subscribe(river.EventKindJobCompleted)
	if err := client.Start(ctx); err != nil {
		cancelCompleted()
		return nil, fmt.Errorf("scheduler: river start: %w", err)
	}
	s.river = client
	// Notify only AFTER River has finalized the row and merged RecordOutput. Emitting from
	// execute() is too early: the frontend would refetch history while the row was still
	// running, miss the just-finished execution, and receive no later signal to try again.
	go func() {
		defer cancelCompleted()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-completed:
				if !ok {
					return
				}
				if _, ok := executionFromRiverRow(event.Job); !ok {
					continue // paused/disabled/legacy rows did not execute work
				}
				var args jobArgs
				if err := json.Unmarshal(event.Job.EncodedArgs, &args); err == nil && args.Name != "" && s.notifier != nil {
					s.notifier.JobChanged(args.Name)
				}
			}
		}
	}()

	// ⚠ **Stop on cancellation, and BLOCK until it finishes.** River's shutdown issues queries,
	// so it must complete while the pool is still open — and the pool is closed by a `defer` in
	// the caller that fires immediately after cancellation. Diagnosed by measurement, not
	// reasoning: with the store closed first, River's Stop returns but its goroutines strand,
	// leaking 4 per generation; closing the store only AFTER the stop leaves the count flat.
	//
	// The §9.2 restart loop rebuilds in-process, so those leaks accumulate across every
	// restart — which is exactly what its goleak test caught here.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := client.Stop(stopCtx); err != nil {
			log.Warn("scheduler: River did not stop cleanly", "err", err)
		}
	}()
	// The returned func is what a caller with its own teardown ordering waits on.
	return func(waitCtx context.Context) error {
		select {
		case <-stopped:
			return nil
		case <-waitCtx.Done():
			return waitCtx.Err()
		}
	}, nil
}

// longQueue holds the jobs that do real media or network work.
const longQueue = "long"

// queueFor decides which queue a job runs on, DERIVED from its declared ceiling.
//
// ⚠ **A queue is a group of jobs that agree to wait for each other, and the group's identity is
// its ceiling.** A job with no declared `Timeout` runs under River's one-minute default and shares
// the `default` queue; a job that declared one gets `long`. A job can therefore never wait longer
// than the ceiling of its own queue.
//
// A short explicit ceiling still belongs on default: system-health needs its ten-second
// cancellation boundary without waiting behind thirty-minute media. Only work allowed to outlive
// River's default is long. The rule remains derived rather than accepting hand-set queue names, so
// `riverQueues` always creates a producer for every possible result.
func queueFor(j Job) string {
	if j.Timeout > river.JobTimeoutDefault {
		return longQueue
	}
	return river.QueueDefault
}

// riverQueues builds the queue set from the registry.
//
// ⚠ **This is the companion the 30-minute ceiling had to arrive with.** `Job.Timeout` fixed jobs
// being SIGKILLed at 60s, and by doing so let one job hold the single SQLite worker for half an
// hour. Measured 2026-08-12: a `filler-pipeline` pass ran 01:50:11Z → 02:20:47Z and every other
// job was starved for its whole duration — channel maintenance, `images-fetch` and `seerr-queue-poll`
// all missed 02:00:00Z, `library-scan` and `reconcile` sat at 01:55:00Z, and a manually triggered
// `filler-sync` did not execute until the worker freed. A ceiling on a shared worker is an outage
// for everything that shares it.
//
// ⚠ `long` is MaxWorkers 1 on BOTH backends, and the reason is not the SQLite pool — it is that
// ffmpeg competes with playout for the GPU, so a media worker POOL would turn a catalog import
// into a live-channel outage (the argument `filler.Pipeline` already records). Total concurrency
// goes 1→2 on SQLite and 4→5 on Postgres: one more slot, reserved for work that spends its time
// in `exec.Command` rather than on the database connection.
func (s *Scheduler) riverQueues(st store.Store) map[string]river.QueueConfig {
	out := map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: maxWorkersFor(st)},
	}
	for _, j := range s.jobs {
		if q := queueFor(j); q != river.QueueDefault {
			out[q] = river.QueueConfig{MaxWorkers: 1}
		}
	}
	return out
}

// maxWorkersFor keeps SQLite single-threaded (one connection in the pool) and lets Postgres
// run a few jobs at once.
func maxWorkersFor(st store.Store) int {
	if store.DialectOf(st) == store.DialectPostgres {
		return 4
	}
	return 1
}

const liveCronPollInterval = 15 * time.Second

// liveCronGate gives River a short, fixed wake-up while deciding from the current cron whether
// that wake-up represents a real scheduled tick. River otherwise computes a cron's next time
// once and sleeps until it, so changing a daily schedule to every minute would still wait until
// the old daily target. The gate keeps settings hot without rebuilding leader-only River state.
type liveCronGate struct {
	scheduler *Scheduler
	job       Job
	mu        sync.Mutex
	lastTick  time.Time
}

func (*liveCronGate) Next(current time.Time) time.Time { return current.Add(liveCronPollInterval) }

func (g *liveCronGate) due(now time.Time) bool {
	parsed, err := cronParser.Parse(g.scheduler.effectiveCron(g.job))
	if err != nil {
		g.scheduler.log.Error("scheduler: live cron parse", "job", g.job.Name, "err", err)
		return false
	}
	// Two poll widths tolerate an enqueuer waking slightly late. lastTick prevents the overlap
	// from inserting the same cron occurrence twice even when the preceding job finished fast.
	tick := parsed.Next(now.Add(-2 * liveCronPollInterval))
	if tick.After(now) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !tick.After(g.lastTick) {
		return false
	}
	g.lastTick = tick
	return true
}

// periodicInsertOpts coalesces scheduled ticks for one named job while preserving explicit
// Run-now requests. River's periodic constructor uses these uniqueness options; TriggerRiver
// deliberately does not. Completed/discarded rows are excluded, so the next real tick is free
// to insert as soon as the previous run is finished.
func periodicInsertOpts(j Job) *river.InsertOpts {
	return &river.InsertOpts{
		Queue: queueFor(j),
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
			},
		},
	}
}

// periodicJobs turns the registry into River periodic jobs, one per enabled job.
//
// A DISABLED job gets no periodic entry at all: it stays listed on the Tasks page carrying its
// reason (that listing is the point — an absent row is also a claim), but nothing schedules it.
func (s *Scheduler) periodicJobs() ([]*river.PeriodicJob, error) {
	out := make([]*river.PeriodicJob, 0, len(s.order))
	for _, name := range s.order {
		j := s.jobs[name]
		if j.Disabled() {
			continue
		}
		_, err := cronParser.Parse(s.effectiveCron(j))
		if err != nil {
			// effectiveCron already falls back to the code default, so reaching here means the
			// DEFAULT is invalid — a programming error worth failing the boot for, not a bad
			// operator value to warn about.
			return nil, fmt.Errorf("scheduler: job %q has an invalid default cron %q: %w", j.Name, j.DefaultCron, err)
		}
		name := name // captured by the constructor below
		gate := &liveCronGate{scheduler: s, job: j}
		out = append(out, river.NewPeriodicJob(
			gate,
			// ⚠ Routed onto the job's own queue, so a media job cannot occupy the slot the cheap
			// sweeps share. Derived from the Job, never hand-set — see `queueFor`.
			func() (river.JobArgs, *river.InsertOpts) {
				if !gate.due(s.now()) {
					return nil, nil
				}
				return jobArgs{Name: name}, periodicInsertOpts(j)
			},
			// ⚠ RunOnStart is deliberately OFF. It fires on every leadership election, so a
			// restart loop would re-run every job each time — and these include a full library
			// sweep and a channel rebuild. The old scheduler ran due-on-start jobs because it
			// had no durable schedule; River keeps one.
			&river.PeriodicJobOpts{RunOnStart: false},
		))
	}
	return out, nil
}

// TriggerRiver enqueues one immediate run of a job ("Run now") through River, so the manual
// path and the scheduled path execute via exactly the same worker.
func (s *Scheduler) TriggerRiver(ctx context.Context, name string) error {
	if s.river == nil {
		return fmt.Errorf("scheduler: river not started")
	}
	// ⚠ **NO `ScheduledAt`.** River has two paths, and they are five seconds apart: a job
	// inserted as AVAILABLE is fetched on the poll (~100ms), while one inserted as SCHEDULED —
	// which any future `ScheduledAt` makes it, including `now+10ms` — waits for the
	// job-scheduler maintenance pass, `JobSchedulerIntervalDefault = 5 * time.Second`.
	//
	// A first cut set `ScheduledAt: now+10ms` to keep the insert clear of a periodic insert
	// landing in the same instant. That "tiny" offset made EVERY Run-now take ~4.97s, measured
	// consistently — a click that reads as broken, and enough to make the poll-availability
	// safety proof (TestScanAvailability_NoWebhook) flaky. The 10ms bought nothing: River
	// deduplicates nothing here, and two inserts of the same kind are simply two jobs.
	// ⚠ **Run-now must take the same queue the schedule would**, and forgetting it here is the easy
	// miss: nothing else in the suite covers this path, and the symptom is one already observed
	// live — a manually triggered `filler-sync` waiting out a 30-minute pipeline pass because it
	// landed on `default` behind the very job it was meant to run beside.
	//
	// An unregistered name resolves to the zero Job and therefore `default`; it has no worker
	// either way, so the queue it lands on is immaterial.
	_, err := s.river.Insert(ctx, jobArgs{Name: name, Manual: true}, &river.InsertOpts{Queue: queueFor(s.jobs[name])})
	return err
}

const (
	jobHistoryWindow      = 24 * time.Hour
	jobHistoryRecentLimit = 5
	jobHistoryScanLimit   = 10_000
)

// History returns the last 24 hours of completed executions for one registered task. It is a
// separate, lazy read from List because River rows are per execution: the collapsed Tasks page
// should not query or aggregate them for all jobs.
func (s *Scheduler) History(ctx context.Context, name string) (JobHistory, error) {
	if _, ok := s.jobs[name]; !ok {
		return JobHistory{}, ErrUnknownJob
	}
	if s.river == nil {
		return JobHistory{}, fmt.Errorf("scheduler: River not started")
	}
	rows, err := s.river.JobList(ctx, river.NewJobListParams().
		Kinds(jobArgs{Name: name}.Kind()).
		States(rivertype.JobStateCompleted).
		OrderBy(river.JobListOrderByTime, river.SortOrderDesc).
		First(jobHistoryScanLimit))
	if err != nil {
		return JobHistory{}, fmt.Errorf("scheduler: list history for %s: %w", name, err)
	}
	return summarizeJobHistory(s.now(), rows.Jobs, len(rows.Jobs) == jobHistoryScanLimit), nil
}

func summarizeJobHistory(now time.Time, rows []*rivertype.JobRow, truncated bool) JobHistory {
	history := JobHistory{WindowStart: now.Add(-jobHistoryWindow), Truncated: truncated}
	var totalDuration int64
	for _, row := range rows {
		execution, ok := executionFromRiverRow(row)
		if !ok {
			continue // pre-feature rows have no trustworthy Loomarr outcome
		}
		if execution.FinishedAt.Before(history.WindowStart) {
			continue
		}
		history.RunCount++
		totalDuration += execution.DurationMs
		if execution.Result == "error" {
			history.FailureCount++
		}
		if len(history.Recent) < jobHistoryRecentLimit {
			history.Recent = append(history.Recent, execution)
		}
	}
	if history.RunCount > 0 {
		history.AverageDurationMs = totalDuration / int64(history.RunCount)
	}
	return history
}

func executionFromRiverRow(row *rivertype.JobRow) (JobExecution, bool) {
	var metadata struct {
		Output *jobExecutionOutput `json:"output"`
	}
	if err := json.Unmarshal(row.Metadata, &metadata); err != nil || metadata.Output == nil {
		return JobExecution{}, false
	}
	out := metadata.Output
	if (out.Result != "ok" && out.Result != "error") || out.StartedAt.IsZero() || out.FinishedAt.IsZero() {
		return JobExecution{}, false
	}
	var args jobArgs
	if err := json.Unmarshal(row.EncodedArgs, &args); err != nil {
		return JobExecution{}, false
	}
	return JobExecution{
		StartedAt: out.StartedAt, FinishedAt: out.FinishedAt, DurationMs: out.DurationMs,
		Result: out.Result, Error: out.Error, Manual: args.Manual,
	}, true
}
