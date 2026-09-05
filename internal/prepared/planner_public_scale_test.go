package prepared_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/media"
	"github.com/loomarr/loomarr/internal/prepared"
)

type scaleResolver struct{ candidates []prepared.Candidate }

func (r scaleResolver) Plan(context.Context, time.Time, time.Time) (prepared.ReadinessPlan, error) {
	return prepared.ReadinessPlan{Candidates: r.candidates}, nil
}

type blockingScalePreparation struct {
	mu       sync.Mutex
	started  []string
	start    chan string
	canceled chan string
	release  chan struct{}
}

func (p *blockingScalePreparation) Prepare(
	ctx context.Context, request prepared.Request,
) (prepared.Publication, error) {
	id := request.Source.SourceID
	p.mu.Lock()
	p.started = append(p.started, id)
	p.mu.Unlock()
	p.start <- id
	select {
	case <-ctx.Done():
		p.canceled <- id
		return prepared.Publication{}, ctx.Err()
	case <-p.release:
		return prepared.Publication{}, nil
	}
}

func TestPlannerPublicSeamScalesOneHundredChannelPriorityAndPreemption(t *testing.T) {
	const capacity = 12
	now := time.Unix(1_000, 0)
	rendition := prepared.RenditionContract{
		VideoCodec: "h264", AudioCodec: "aac", Width: 1280, Height: 720,
		FrameRate: 25, VideoBitrateKbps: 5000, AudioBitrateKbps: 160,
		SegmentDurationMS: 2000, PackagingVersion: 1,
	}
	candidates := make([]prepared.Candidate, 0, 101)
	for i := range 100 {
		class := prepared.CandidateLookahead
		switch {
		case i < 20:
			class = prepared.CandidateCurrent
		case i < 40:
			class = prepared.CandidateNext
		}
		candidates = append(candidates, prepared.Candidate{
			Class: class, NeededAt: now.Add(time.Duration(i) * time.Minute),
			Request: prepared.Request{
				Source: prepared.Source{
					ItemID: "item-" + fmt.Sprintf("%03d", i), SourceID: fmt.Sprintf("source-%03d", i),
					Revision: "revision-1",
				},
				Rendition: rendition,
			},
		})
	}
	// Reverse the resolver output and add a duplicate that would consume one of the first eleven
	// slots if requests were not deduplicated. Planner ordering—not fixture order—must still admit
	// the earliest eleven unique current publications.
	for left, right := 0, len(candidates)-1; left < right; left, right = left+1, right-1 {
		candidates[left], candidates[right] = candidates[right], candidates[left]
	}
	duplicate := candidates[len(candidates)-1]
	duplicate.Class = prepared.CandidateCurrent
	duplicate.NeededAt = now.Add(-time.Minute)
	candidates = append(candidates, duplicate)

	work := &blockingScalePreparation{
		start: make(chan string, capacity-1), canceled: make(chan string, 1), release: make(chan struct{}),
	}
	pool := media.NewEncodePool(func() int { return capacity })
	planner := prepared.NewPlanner(prepared.PlannerDependencies{
		Resolver: scaleResolver{candidates: candidates}, Preparation: work, Pool: pool,
		Now: func() time.Time { return now },
	})
	done := make(chan error, 1)
	go func() { done <- planner.Run(t.Context()) }()
	started := make(map[string]bool, capacity-1)
	for range capacity - 1 {
		select {
		case id := <-work.start:
			started[id] = true
		case <-time.After(time.Second):
			t.Fatal("planner did not fill measured N-1 capacity")
		}
	}
	for i := range capacity - 1 {
		id := fmt.Sprintf("source-%03d", i)
		if !started[id] {
			t.Fatalf("initial admitted set = %v, missing urgent %s", started, id)
		}
	}

	reserveRelease, ok := pool.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("foreground reserve was not available")
	}
	secondReleaseCh := make(chan func(), 1)
	go func() {
		release, admitted := pool.AcquireForeground(t.Context())
		if !admitted {
			secondReleaseCh <- nil
			return
		}
		secondReleaseCh <- release
	}()
	select {
	case canceled := <-work.canceled:
		if canceled != "source-010" {
			t.Fatalf("preempted %s, want farthest-needed admitted source-010", canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground did not preempt background work")
	}
	secondRelease := <-secondReleaseCh
	if secondRelease == nil {
		t.Fatal("foreground was not admitted after preemption")
	}
	reserveRelease()
	secondRelease()
	close(work.release)
	if err := <-done; err != nil {
		t.Fatalf("foreground preemption became an operator-visible planner failure: %v", err)
	}
	work.mu.Lock()
	startedCount := len(work.started)
	work.mu.Unlock()
	if startedCount != capacity-1 {
		t.Fatalf("planner started %d preparations after preemption, want only initial %d", startedCount, capacity-1)
	}
}
