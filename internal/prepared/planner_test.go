package prepared

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/media"
)

func testSource(id string, audioTrack ...int) Source {
	track := 0
	if len(audioTrack) > 0 {
		track = audioTrack[0]
	}
	return Source{ItemID: "item-" + id, SourceID: "source-" + id, Revision: "revision-" + id, AudioTrack: track}
}

type fixedCandidates struct {
	items     []Candidate
	protected []Specification
	summary   ReadinessSummary
	err       error
}

type countingCandidates struct {
	calls atomic.Int64
	items []Candidate
}

type observingCandidates struct {
	before     ReadinessPlan
	after      ReadinessPlan
	observeErr error
	observeCtx error
	calls      atomic.Int64
}

func (f *observingCandidates) Plan(context.Context, time.Time, time.Time) (ReadinessPlan, error) {
	f.calls.Add(1)
	return f.before, nil
}

func (f *observingCandidates) Observe(ctx context.Context, _ time.Time, _ time.Time) (ReadinessPlan, error) {
	f.calls.Add(1)
	f.observeCtx = ctx.Err()
	return f.after, f.observeErr
}

func (f *countingCandidates) Plan(context.Context, time.Time, time.Time) (ReadinessPlan, error) {
	f.calls.Add(1)
	return ReadinessPlan{Candidates: f.items}, nil
}

func (f fixedCandidates) Plan(context.Context, time.Time, time.Time) (ReadinessPlan, error) {
	return ReadinessPlan{Candidates: f.items, Protected: f.protected, Summary: f.summary}, f.err
}

type recordingPreparation struct {
	mu       sync.Mutex
	requests []Request
	run      func(context.Context, Request) error
}

func (p *recordingPreparation) Prepare(ctx context.Context, request Request) (Publication, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if p.run != nil {
		return Publication{}, p.run(ctx, request)
	}
	return Publication{}, nil
}

func TestPlannerPreparesUniqueCandidatesInNeedOrder(t *testing.T) {
	now := time.Unix(1_000, 0)
	a := Request{Source: testSource("a"), Rendition: baselineRendition()}
	b := Request{Source: testSource("b", 1), Rendition: baselineRendition()}
	work := &recordingPreparation{}
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{items: []Candidate{
			{NeededAt: now.Add(time.Hour), Request: b},
			{NeededAt: now.Add(2 * time.Hour), Request: a},
			{NeededAt: now, Request: a},
		}}, Preparation: work, Pool: media.NewEncodePool(func() int { return 2 }),
		Now: func() time.Time { return now },
	})

	if err := p.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(work.requests) != 2 || work.requests[0] != a || work.requests[1] != b {
		t.Fatalf("requests = %#v, want [a b] in earliest-need order", work.requests)
	}
}

func TestPlannerFillsAndRefillsMeasuredBackgroundCapacity(t *testing.T) {
	const (
		capacity   = 12
		candidates = 100
	)
	now := time.Unix(1_000, 0)
	items := make([]Candidate, candidates)
	for i := range items {
		items[i] = Candidate{
			NeededAt: now.Add(time.Duration(i) * time.Second),
			Request:  Request{Source: testSource(string(rune(i + 1))), Rendition: baselineRendition()},
		}
	}
	started := make(chan struct{}, capacity-1)
	releaseFirstWave := make(chan struct{})
	var calls atomic.Int64
	var active atomic.Int64
	var peak atomic.Int64
	work := &recordingPreparation{run: func(context.Context, Request) error {
		call := calls.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			prior := peak.Load()
			if prior >= current || peak.CompareAndSwap(prior, current) {
				break
			}
		}
		if call <= capacity-1 {
			started <- struct{}{}
			<-releaseFirstWave
		}
		return nil
	}}
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{items: items}, Preparation: work,
		Pool: media.NewEncodePool(func() int { return capacity }), Now: func() time.Time { return now },
	})
	done := make(chan error, 1)
	go func() { done <- p.Run(t.Context()) }()
	for range capacity - 1 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("planner did not fill measured background capacity")
		}
	}
	if got := peak.Load(); got != capacity-1 {
		t.Fatalf("first-wave concurrency = %d, want %d", got, capacity-1)
	}
	close(releaseFirstWave)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != candidates {
		t.Fatalf("preparation calls = %d, want %d after refill", got, candidates)
	}
	if got := peak.Load(); got != capacity-1 {
		t.Fatalf("peak preparation concurrency = %d, want %d", got, capacity-1)
	}
}

func TestPlannerCoalescesOverlappingRuns(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	resolver := &countingCandidates{items: []Candidate{{
		Request: Request{Source: testSource("warming"), Rendition: baselineRendition()},
	}}}
	p := NewPlanner(PlannerDependencies{
		Resolver: resolver,
		Preparation: &recordingPreparation{run: func(context.Context, Request) error {
			close(started)
			<-finish
			return nil
		}},
		Pool: media.NewEncodePool(func() int { return 2 }), Now: time.Now,
	})
	first := make(chan error, 1)
	go func() { first <- p.Run(t.Context()) }()
	<-started
	if err := p.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("resolver calls during overlap = %d, want one coalesced pass", got)
	}
	if got := p.Status(); !got.Running {
		t.Fatalf("overlapping yield cleared in-progress status: %+v", got)
	}
	close(finish)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestPlannerYieldsWhenLiveOwnsTheSpareCapacity(t *testing.T) {
	pool := media.NewEncodePool(func() int { return 2 })
	release, ok := pool.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("foreground setup lease refused")
	}
	defer release()
	work := &recordingPreparation{}
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{items: []Candidate{
			{Request: Request{Source: testSource("a"), Rendition: baselineRendition()}},
		}},
		Preparation: work, Pool: pool, Now: time.Now,
	})

	if err := p.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(work.requests) != 0 {
		t.Fatal("planner started work while live playout owned the spare slot")
	}
}

func TestPlannerTreatsForegroundPreemptionAsAYield(t *testing.T) {
	pool := media.NewEncodePool(func() int { return 2 })
	started := make(chan struct{})
	work := &recordingPreparation{run: func(ctx context.Context, _ Request) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{items: []Candidate{
			{Request: Request{Source: testSource("a"), Rendition: baselineRendition()}},
		}},
		Preparation: work, Pool: pool, Now: time.Now,
	})

	done := make(chan error, 1)
	go func() { done <- p.Run(t.Context()) }()
	<-started
	firstForegroundRelease, ok := pool.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("first foreground lease refused")
	}
	foregroundRelease, ok := pool.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("foreground could not preempt preparation")
	}
	firstForegroundRelease()
	foregroundRelease()
	if err := <-done; err != nil {
		t.Fatalf("preempted planner returned an operator-visible failure: %v", err)
	}
}

func TestPlannerForegroundPreemptionStopsRefillAcrossMeasuredCapacity(t *testing.T) {
	const capacity = 4
	now := time.Unix(1_000, 0)
	items := make([]Candidate, 10)
	for i := range items {
		items[i] = Candidate{
			NeededAt: now.Add(time.Duration(i) * time.Minute),
			Request:  Request{Source: testSource(fmt.Sprintf("%02d", i)), Rendition: baselineRendition()},
		}
	}
	started := make(chan struct{}, capacity-1)
	finish := make(chan struct{})
	var calls atomic.Int64
	work := &recordingPreparation{run: func(ctx context.Context, _ Request) error {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finish:
			return nil
		}
	}}
	pool := media.NewEncodePool(func() int { return capacity })
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{items: items}, Preparation: work, Pool: pool,
		Now: func() time.Time { return now },
	})
	done := make(chan error, 1)
	go func() { done <- p.Run(t.Context()) }()
	for range capacity - 1 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("planner did not fill the background pool")
		}
	}
	reserveRelease, ok := pool.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("live request could not take the reserved slot")
	}
	second := make(chan func(), 1)
	go func() {
		release, admitted := pool.AcquireForeground(t.Context())
		if !admitted {
			second <- nil
			return
		}
		second <- release
	}()
	var preemptedRelease func()
	select {
	case preemptedRelease = <-second:
		if preemptedRelease == nil {
			t.Fatal("live request was not admitted after preparation preemption")
		}
	case <-time.After(time.Second):
		t.Fatal("live request did not promptly preempt preparation")
	}
	reserveRelease()
	preemptedRelease()
	close(finish)
	if err := <-done; err != nil {
		t.Fatalf("preempted planner returned an operator-visible failure: %v", err)
	}
	if got := calls.Load(); got != capacity-1 {
		t.Fatalf("preparation calls after live preemption = %d, want initial wave %d with no refill", got, capacity-1)
	}
}

func TestPlannerContinuesPastOneBadSource(t *testing.T) {
	now := time.Unix(1_000, 0)
	bad := Request{Source: testSource("bad"), Rendition: baselineRendition()}
	good := Request{Source: testSource("good"), Rendition: baselineRendition()}
	work := &recordingPreparation{run: func(_ context.Context, request Request) error {
		if request == bad {
			return errors.New("broken source")
		}
		return nil
	}}
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{items: []Candidate{
			{NeededAt: now, Request: bad}, {NeededAt: now.Add(time.Minute), Request: good},
		}}, Preparation: work, Pool: media.NewEncodePool(func() int { return 2 }),
		Now: func() time.Time { return now },
	})

	err := p.Run(t.Context())
	if err == nil || len(work.requests) != 2 {
		t.Fatalf("Run error = %v, requests = %d; want error plus continued progress", err, len(work.requests))
	}
}

type recordingRetainer struct {
	calls     int
	budget    int64
	protected []Specification
	ctxErr    error
	result    PruneResult
	err       error
}

func (r *recordingRetainer) Prune(
	ctx context.Context, budget int64, protected []Specification,
) (PruneResult, error) {
	r.calls++
	r.budget = budget
	r.protected = append([]Specification(nil), protected...)
	r.ctxErr = ctx.Err()
	return r.result, r.err
}

func TestPlannerRunsRetentionAfterYieldingPreparation(t *testing.T) {
	pool := media.NewEncodePool(func() int { return 1 }) // no background slot by contract
	retainer := &recordingRetainer{result: PruneResult{RemainingBytes: 700, BudgetBytes: 512}}
	protected := Specification{SourceFingerprint: "scheduled", Rendition: baselineRendition()}
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{protected: []Specification{protected}, items: []Candidate{
			{Request: Request{Source: testSource("a"), Rendition: baselineRendition()}},
		}},
		Preparation: &recordingPreparation{}, Pool: pool, Retainer: retainer,
		BudgetBytes: func() int64 { return 512 }, Now: time.Now,
	})

	if err := p.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if retainer.calls != 1 || retainer.budget != 512 || len(retainer.protected) != 1 || retainer.protected[0] != protected {
		t.Fatalf("retention calls = %d at %d bytes, want one at 512", retainer.calls, retainer.budget)
	}
}

func TestPlannerStatusReportsResolvedReadinessAndRetentionAfterYield(t *testing.T) {
	now := time.Unix(20_000, 0)
	retainer := &recordingRetainer{result: PruneResult{
		RemainingBytes: 700, BudgetBytes: 512, ProtectedBytes: 400,
		PublicationsEvicted: 2, BytesEvicted: 100,
	}}
	wantReadiness := ReadinessSummary{
		Channels: 100, ReadyChannels: 84,
		ScheduledBindings: 300, ReadyBindings: 260, MissingBindings: 40,
		QueuedPublications: 16,
	}
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{summary: wantReadiness, items: []Candidate{{
			Request: Request{Source: testSource("warming"), Rendition: baselineRendition()},
		}}},
		Preparation: &recordingPreparation{},
		Pool:        media.NewEncodePool(func() int { return 1 }), // no background slot: a normal yield
		Retainer:    retainer,
		BudgetBytes: func() int64 { return 512 },
		Now:         func() time.Time { return now },
	})

	if got := p.Status(); !got.Available || got.Running || !got.LastRunAt.IsZero() {
		t.Fatalf("initial status = %+v, want available but not yet run", got)
	}
	if err := p.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := PlannerStatus{
		Available: true, LastRunAt: now, Readiness: wantReadiness,
		Retention: RetentionStatus{
			RemainingBytes: 700, BudgetBytes: 512, ProtectedBytes: 400,
			PublicationsEvicted: 2, BytesEvicted: 100,
		},
	}
	if got := p.Status(); got != want {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
}

func TestPlannerStatusObservesResultingReadinessAfterWork(t *testing.T) {
	now := time.Unix(21_000, 0)
	request := Request{Source: testSource("warming"), Rendition: baselineRendition()}
	before := ReadinessPlan{
		Candidates: []Candidate{{Request: request}},
		Summary: ReadinessSummary{
			Channels: 100, ScheduledBindings: 100, MissingBindings: 100, QueuedPublications: 100,
		},
	}
	after := ReadinessPlan{Summary: ReadinessSummary{
		Channels: 100, ReadyChannels: 100, ScheduledBindings: 100, ReadyBindings: 100,
	}}
	resolver := &observingCandidates{before: before, after: after}
	p := NewPlanner(PlannerDependencies{
		Resolver: resolver, Preparation: &recordingPreparation{},
		Pool: media.NewEncodePool(func() int { return 2 }), Now: func() time.Time { return now },
	})

	if err := p.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := p.Status().Readiness; got != after.Summary {
		t.Fatalf("post-work readiness = %+v, want resulting %+v", got, after.Summary)
	}
	if got := resolver.calls.Load(); got != 2 {
		t.Fatalf("resolver calls = %d, want plan plus lookup-only observation", got)
	}
}

func TestPlannerDeadlineReserveDrainsIntoObservationAndRetention(t *testing.T) {
	now := time.Unix(21_500, 0)
	request := Request{Source: testSource("deferred"), Rendition: baselineRendition()}
	beforeProtected := Specification{SourceFingerprint: "before", Rendition: baselineRendition()}
	afterProtected := Specification{SourceFingerprint: "after", Rendition: baselineRendition()}
	resolver := &observingCandidates{
		before: ReadinessPlan{
			Candidates: []Candidate{{Request: request}}, Protected: []Specification{beforeProtected},
		},
		after: ReadinessPlan{Protected: []Specification{afterProtected}},
	}
	work := &recordingPreparation{}
	retainer := &recordingRetainer{}
	p := NewPlanner(PlannerDependencies{
		Resolver: resolver, Preparation: work, Pool: media.NewEncodePool(func() int { return 2 }),
		Retainer: retainer, BudgetBytes: func() int64 { return 512 }, Now: func() time.Time { return now },
	})
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(preparationDrainReserve-time.Minute))
	defer cancel()

	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(work.requests) != 0 {
		t.Fatal("planner started preparation inside its drain reserve")
	}
	if got := resolver.calls.Load(); got != 2 {
		t.Fatalf("resolver calls = %d, want plan plus post-drain observation", got)
	}
	if retainer.calls != 1 || len(retainer.protected) != 1 || retainer.protected[0] != afterProtected {
		t.Fatalf("retention protected = %+v, want post-observation hot set", retainer.protected)
	}
}

type expiringPlannerContext struct {
	context.Context
	deadline time.Time
	done     chan struct{}
	expired  atomic.Bool
	once     sync.Once
}

func newExpiringPlannerContext(parent context.Context) *expiringPlannerContext {
	return &expiringPlannerContext{
		Context: parent, deadline: time.Now().Add(preparationDrainReserve + time.Minute),
		done: make(chan struct{}),
	}
}

func (c *expiringPlannerContext) Deadline() (time.Time, bool) { return c.deadline, true }
func (c *expiringPlannerContext) Done() <-chan struct{}       { return c.done }
func (c *expiringPlannerContext) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}
func (c *expiringPlannerContext) expire() {
	c.once.Do(func() {
		c.expired.Store(true)
		close(c.done)
	})
}

func TestPlannerDeadlineDuringWorkStillObservesAndRetains(t *testing.T) {
	now := time.Unix(21_625, 0)
	request := Request{Source: testSource("deadline"), Rendition: baselineRendition()}
	protected := Specification{SourceFingerprint: "current", Rendition: baselineRendition()}
	resolver := &observingCandidates{
		before: ReadinessPlan{Candidates: []Candidate{{Request: request}}},
		after:  ReadinessPlan{Protected: []Specification{protected}},
	}
	retainer := &recordingRetainer{}
	ctx := newExpiringPlannerContext(t.Context())
	p := NewPlanner(PlannerDependencies{
		Resolver: resolver,
		Preparation: &recordingPreparation{run: func(workCtx context.Context, _ Request) error {
			ctx.expire()
			<-workCtx.Done()
			return workCtx.Err()
		}},
		Pool: media.NewEncodePool(func() int { return 2 }), Retainer: retainer,
		BudgetBytes: func() int64 { return 512 }, Now: func() time.Time { return now },
	})

	err := p.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want deadline exceeded after finalization", err)
	}
	if got := resolver.calls.Load(); got != 2 || resolver.observeCtx != nil {
		t.Fatalf("post-deadline observation = calls %d ctx %v, want one live finalization call", got, resolver.observeCtx)
	}
	if retainer.calls != 1 || retainer.ctxErr != nil || len(retainer.protected) != 1 || retainer.protected[0] != protected {
		t.Fatalf("post-deadline retention = calls %d ctx %v protected %+v", retainer.calls, retainer.ctxErr, retainer.protected)
	}
}

func TestPlannerObservationFailurePreservesTheResolvedHotSet(t *testing.T) {
	now := time.Unix(21_750, 0)
	protected := Specification{SourceFingerprint: "current", Rendition: baselineRendition()}
	wantSummary := ReadinessSummary{Channels: 100, ReadyChannels: 99, MissingBindings: 1}
	resolver := &observingCandidates{
		before:     ReadinessPlan{Protected: []Specification{protected}, Summary: wantSummary},
		after:      ReadinessPlan{},
		observeErr: errors.New("transient schedule read"),
	}
	retainer := &recordingRetainer{}
	p := NewPlanner(PlannerDependencies{
		Resolver: resolver, Preparation: &recordingPreparation{},
		Pool: media.NewEncodePool(func() int { return 2 }), Retainer: retainer,
		BudgetBytes: func() int64 { return 512 }, Now: func() time.Time { return now },
	})

	err := p.Run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "transient schedule read") {
		t.Fatalf("Run error = %v, want observation failure", err)
	}
	if retainer.calls != 1 || len(retainer.protected) != 1 || retainer.protected[0] != protected {
		t.Fatalf("retention lost pre-work hot set after observation failure: %+v", retainer.protected)
	}
	if got := p.Status().Readiness; got != wantSummary {
		t.Fatalf("status after failed observation = %+v, want last complete plan %+v", got, wantSummary)
	}
}

func TestPlannerStatusReportsPassInProgress(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	p := NewPlanner(PlannerDependencies{
		Resolver: fixedCandidates{items: []Candidate{{
			Request: Request{Source: testSource("warming"), Rendition: baselineRendition()},
		}}},
		Preparation: &recordingPreparation{run: func(context.Context, Request) error {
			close(started)
			<-finish
			return nil
		}},
		Pool: media.NewEncodePool(func() int { return 2 }), Now: time.Now,
	})
	done := make(chan error, 1)
	go func() { done <- p.Run(t.Context()) }()
	<-started
	if got := p.Status(); !got.Running || !got.LastRunAt.IsZero() {
		t.Fatalf("in-progress status = %+v", got)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := p.Status(); got.Running || got.LastRunAt.IsZero() {
		t.Fatalf("completed status = %+v", got)
	}
}

func TestPlannerStatusPreservesUnavailableReason(t *testing.T) {
	p := NewPlanner(PlannerDependencies{UnavailableReason: "prepared volume is read-only"})
	if got := p.Status(); got.Available || got.UnavailableReason != "prepared volume is read-only" {
		t.Fatalf("status = %+v", got)
	}
}
