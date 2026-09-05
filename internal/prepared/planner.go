package prepared

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/media"
)

const preparationLookahead = 6 * time.Hour

// preparationDrainReserve leaves enough of River's media-job deadline to observe the resulting
// readiness and run retention after active encoders exit. It is intentionally internal policy, not
// another operator tuning surface.
const preparationDrainReserve = 10 * time.Minute

// preparationFinalizationTimeout bounds the narrow recovery path used only when an active encoder
// consumes the River deadline despite the launch reserve. It gives lookup-only observation and
// retention a final cancellation window without detaching ordinary shutdown cancellation.
const preparationFinalizationTimeout = 30 * time.Second

// CandidateClass orders the bounded readiness frontier. Zero is current so older callers and test
// fixtures that omit the class remain maximally urgent.
type CandidateClass uint8

const (
	CandidateCurrent CandidateClass = iota
	CandidateNext
	CandidateLookahead
)

// Candidate is one immutable source/rendition needed by the accepted schedule. Class then NeededAt
// control priority; Channel identity is deliberately absent because publications are shared.
type Candidate struct {
	Class    CandidateClass
	NeededAt time.Time
	Request  Request
}

// ReadinessPlan separates missing work from the complete accepted schedule. Protected includes
// every ready publication still referenced by that schedule, even when no encoder slot is free.
type ReadinessPlan struct {
	Candidates []Candidate
	Protected  []Specification
	Summary    ReadinessSummary
}

// ReadinessSummary is the schedule-level result of one resolved lookahead window. Bindings count
// Channel/item pairs rather than publications because one shared publication may make many
// Channels ready.
type ReadinessSummary struct {
	Channels           int
	ReadyChannels      int
	ScheduledBindings  int
	ReadyBindings      int
	MissingBindings    int
	QueuedPublications int
}

// RetentionStatus is the durable store result from the same scheduler pass as readiness.
type RetentionStatus struct {
	RemainingBytes      int64
	BudgetBytes         int64
	ProtectedBytes      int64
	PublicationsEvicted int
	BytesEvicted        int64
	StagingRemoved      int
}

// PlannerStatus is the planner-owned operational snapshot projected by the playout status API.
// A zero LastRunAt means no pass has completed; zero counts must not be interpreted as all ready.
type PlannerStatus struct {
	Available         bool
	UnavailableReason string
	Running           bool
	LastRunAt         time.Time
	LastError         string
	Readiness         ReadinessSummary
	Retention         RetentionStatus
}

// CandidateResolver reads the authoritative schedule and returns stable Inventory sources.
// Implemented at composition, where channels, source access, and audio selection meet.
type CandidateResolver interface {
	Plan(context.Context, time.Time, time.Time) (ReadinessPlan, error)
}

// ReadinessObserver is the lookup-only post-work half implemented by the runtime resolver. Keeping
// it optional preserves narrow test and embedded resolvers while production status reports the
// state resulting from this pass rather than the state before it.
type ReadinessObserver interface {
	Observe(context.Context, time.Time, time.Time) (ReadinessPlan, error)
}

// Preparation publishes one request. Preparer implements it; the interface keeps Planner focused
// on schedule priority rather than fingerprinting or packaging internals.
type Preparation interface {
	Prepare(context.Context, Request) (Publication, error)
}

// Retainer owns the lifecycle of the same immutable store preparation writes.
type Retainer interface {
	Prune(context.Context, int64, []Specification) (PruneResult, error)
}

// PlannerDependencies makes the readiness control plane's ownership explicit. Preparation and
// retention intentionally share one scheduler pass so lifecycle cannot drift into a bolt-on task.
type PlannerDependencies struct {
	Resolver          CandidateResolver
	Preparation       Preparation
	Pool              *media.EncodePool
	Retainer          Retainer
	BudgetBytes       func() int64
	Now               func() time.Time
	Log               *slog.Logger
	UnavailableReason string
}

// Planner is the readiness control plane. It can publish media but cannot mutate a schedule, and
// every encode runs under the shared preemptible background lease.
type Planner struct {
	resolver CandidateResolver
	preparer Preparation
	pool     *media.EncodePool
	retainer Retainer
	budget   func() int64
	now      func() time.Time
	log      *slog.Logger
	runMu    sync.Mutex
	statusMu sync.RWMutex
	status   PlannerStatus
}

func NewPlanner(deps PlannerDependencies) *Planner {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	available := deps.UnavailableReason == "" && deps.Resolver != nil && deps.Preparation != nil && deps.Pool != nil
	reason := deps.UnavailableReason
	if !available && reason == "" {
		reason = "the prepared playout planner is not wired"
	}
	return &Planner{
		resolver: deps.Resolver, preparer: deps.Preparation, pool: deps.Pool,
		retainer: deps.Retainer, budget: deps.BudgetBytes, now: deps.Now, log: deps.Log,
		status: PlannerStatus{Available: available, UnavailableReason: reason},
	}
}

// Status returns the latest immutable operational snapshot without touching the schedule or disk.
func (p *Planner) Status() PlannerStatus {
	if p == nil {
		return PlannerStatus{UnavailableReason: "the prepared playout planner is not wired"}
	}
	p.statusMu.RLock()
	defer p.statusMu.RUnlock()
	return p.status
}

// Run prepares as much of the next schedule window as spare hardware permits. A foreground
// preemption or a busy pool is a normal yield, not a failed task. Independent source failures are
// joined after the pass so one corrupt file cannot starve every later candidate.
func (p *Planner) Run(ctx context.Context) (runErr error) {
	if p == nil {
		return nil
	}
	if !p.runMu.TryLock() {
		return nil
	}
	defer p.runMu.Unlock()
	p.statusMu.Lock()
	p.status.Running = true
	p.statusMu.Unlock()
	var errs []error
	var plan ReadinessPlan
	var retention PruneResult
	finalizationCtx := ctx
	defer func() {
		p.statusMu.Lock()
		p.status.Running = false
		p.status.LastRunAt = p.now()
		p.status.Readiness = plan.Summary
		p.status.Retention = retentionStatusFrom(retention)
		p.status.LastError = ""
		if runErr != nil {
			p.status.LastError = runErr.Error()
		}
		p.statusMu.Unlock()
	}()
	if p.resolver != nil && p.preparer != nil && p.pool != nil {
		now := p.now()
		var resolveErr error
		plan, resolveErr = p.resolver.Plan(ctx, now, now.Add(preparationLookahead))
		errs = append(errs, resolveErr)
		preparationErrs := p.prepare(ctx, uniqueCandidates(plan.Candidates))
		errs = append(errs, preparationErrs...)
		if ctxErr := ctx.Err(); ctxErr != nil {
			if !errors.Is(ctxErr, context.DeadlineExceeded) {
				return ctxErr
			}
			errs = append(errs, ctxErr)
			var cancel context.CancelFunc
			finalizationCtx, cancel = context.WithTimeout(
				context.WithoutCancel(ctx), preparationFinalizationTimeout,
			)
			defer cancel()
		}
		if observer, ok := p.resolver.(ReadinessObserver); ok {
			observed, observeErr := observer.Observe(finalizationCtx, now, now.Add(preparationLookahead))
			if observeErr == nil {
				plan = observed
			}
			errs = append(errs, observeErr)
		}
	}
	if p.retainer != nil && p.budget != nil {
		budget := p.budget()
		result, err := p.retainer.Prune(finalizationCtx, budget, plan.Protected)
		retention = result
		if err != nil {
			errs = append(errs, fmt.Errorf("retain prepared media: %w", err))
		}
		fields := []any{
			"bytes", result.RemainingBytes, "budget", result.BudgetBytes,
			"protected_bytes", result.ProtectedBytes,
			"publications_evicted", result.PublicationsEvicted,
			"bytes_evicted", result.BytesEvicted, "staging_removed", result.StagingRemoved,
		}
		if result.RemainingBytes > result.BudgetBytes && result.BudgetBytes > 0 {
			p.log.Warn("prepared media remains over its soft budget because recent playback is protected", fields...)
		} else {
			p.log.Info("prepared media retention pass", fields...)
		}
	}
	runErr = errors.Join(errs...)
	return runErr
}

type preparationResult struct {
	order     int
	sourceID  string
	err       error
	preempted bool
}

// prepare keeps the pool full while useful job time remains, then drains every worker before the
// caller observes readiness or prunes the store. One foreground preemption stops new launches for
// this pass: the cancelled urgent frontier remains ahead of less urgent work on the next tick.
func (p *Planner) prepare(ctx context.Context, candidates []Candidate) []error {
	results := make(chan preparationResult, len(candidates))
	next, active := 0, 0
	launching := true
	completed := make([]preparationResult, 0)
	for next < len(candidates) || active > 0 {
		if launching && next < len(candidates) && usefulPreparationTime(ctx) {
			candidate := candidates[next]
			workCtx, release, ok := p.pool.AcquireBackground(ctx, candidate.NeededAt)
			if ok {
				order := next
				next++
				active++
				go func() {
					_, err := p.preparer.Prepare(workCtx, candidate.Request)
					preempted := ctx.Err() == nil && errors.Is(err, context.Canceled) && workCtx.Err() != nil
					release()
					results <- preparationResult{
						order: order, sourceID: candidate.Request.Source.SourceID,
						err: err, preempted: preempted,
					}
				}()
				continue
			}
		}
		if active == 0 {
			break
		}
		result := <-results
		active--
		if result.preempted {
			launching = false
			continue
		}
		if result.err != nil {
			completed = append(completed, result)
		}
	}
	slices.SortFunc(completed, func(a, b preparationResult) int { return a.order - b.order })
	errs := make([]error, 0, len(completed))
	for _, result := range completed {
		errs = append(errs, fmt.Errorf("prepare source %q: %w", result.sourceID, result.err))
	}
	return errs
}

func uniqueCandidates(candidates []Candidate) []Candidate {
	candidates = append([]Candidate(nil), candidates...)
	slices.SortStableFunc(candidates, compareCandidates)
	seen := make(map[Request]struct{}, len(candidates))
	unique := candidates[:0]
	for _, candidate := range candidates {
		if _, duplicate := seen[candidate.Request]; duplicate {
			continue
		}
		seen[candidate.Request] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique
}

func compareCandidates(a, b Candidate) int {
	if a.Class < b.Class {
		return -1
	}
	if a.Class > b.Class {
		return 1
	}
	return a.NeededAt.Compare(b.NeededAt)
}

func usefulPreparationTime(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	deadline, bounded := ctx.Deadline()
	return !bounded || time.Until(deadline) > preparationDrainReserve
}

func retentionStatusFrom(result PruneResult) RetentionStatus {
	return RetentionStatus{
		RemainingBytes: result.RemainingBytes, BudgetBytes: result.BudgetBytes,
		ProtectedBytes: result.ProtectedBytes, PublicationsEvicted: result.PublicationsEvicted,
		BytesEvicted: result.BytesEvicted, StagingRemoved: result.StagingRemoved,
	}
}
