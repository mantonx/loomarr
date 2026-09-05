package media

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEncodePoolForegroundUsesEverySlot(t *testing.T) {
	p := NewEncodePool(func() int { return 2 })

	r1, ok1 := p.AcquireForeground(t.Context())
	r2, ok2 := p.AcquireForeground(t.Context())
	if !ok1 || !ok2 {
		t.Fatalf("first two foreground leases = %v, %v; want both admitted", ok1, ok2)
	}
	if _, ok := p.AcquireForeground(t.Context()); ok {
		t.Fatal("third foreground lease admitted past capacity")
	}
	r1()
	r2()
}

func TestEncodePoolBackgroundKeepsOneSlotForForeground(t *testing.T) {
	p := NewEncodePool(func() int { return 4 })

	releases := make([]func(), 0, 3)
	for i := range 3 {
		workCtx, release, ok := p.AcquireBackground(t.Context(), time.Unix(int64(i), 0))
		if !ok {
			t.Fatalf("background lease %d refused with measured spare capacity", i)
		}
		if err := workCtx.Err(); err != nil {
			t.Fatalf("background context %d already cancelled: %v", i, err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	if _, _, ok := p.AcquireBackground(t.Context(), time.Unix(3, 0)); ok {
		t.Fatal("a fourth background encode consumed the foreground reserve")
	}
	foregroundRelease, ok := p.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("background work consumed the foreground reserve")
	}
	foregroundRelease()
}

func TestEncodePoolForegroundPreemptsFarthestNeededBackground(t *testing.T) {
	p := NewEncodePool(func() int { return 3 })
	now := time.Now()

	urgentCtx, urgentRelease, ok := p.AcquireBackground(t.Context(), now)
	if !ok {
		t.Fatal("urgent background lease refused")
	}
	defer urgentRelease()
	laterCtx, laterRelease, ok := p.AcquireBackground(t.Context(), now.Add(time.Hour))
	if !ok {
		t.Fatal("later background lease refused")
	}
	foregroundRelease, ok := p.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("first foreground lease refused")
	}

	released := make(chan struct{})
	go func() {
		<-laterCtx.Done()
		laterRelease()
		close(released)
	}()

	secondRelease, ok := p.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("second foreground lease did not replace cancelled background work")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("preempted background worker did not observe cancellation")
	}
	if urgentCtx.Err() != nil {
		t.Fatal("foreground preempted the most urgent background worker")
	}
	foregroundRelease()
	secondRelease()
}

func TestEncodePoolConcurrentForegroundCancelsOnlyOutstandingDemand(t *testing.T) {
	const (
		capacity = 8
		extra    = 3
	)
	p := NewEncodePool(func() int { return capacity })
	var canceled atomic.Int64
	backgroundReleases := make([]func(), 0, capacity-1)
	var backgroundWorkers sync.WaitGroup
	for i := range capacity - 1 {
		workCtx, release, ok := p.AcquireBackground(t.Context(), time.Unix(int64(i), 0))
		if !ok {
			t.Fatalf("background setup lease %d refused", i)
		}
		backgroundReleases = append(backgroundReleases, release)
		backgroundWorkers.Add(1)
		go func() {
			defer backgroundWorkers.Done()
			<-workCtx.Done()
			canceled.Add(1)
			release()
		}()
	}
	reserveRelease, ok := p.AcquireForeground(t.Context())
	if !ok {
		t.Fatal("foreground reserve refused")
	}
	defer reserveRelease()

	admissions := make(chan bool, extra)
	releaseForeground := make(chan struct{})
	var foregroundWorkers sync.WaitGroup
	for range extra {
		foregroundWorkers.Add(1)
		go func() {
			defer foregroundWorkers.Done()
			release, admitted := p.AcquireForeground(t.Context())
			admissions <- admitted
			if admitted {
				<-releaseForeground
				release()
			}
		}()
	}
	for range extra {
		select {
		case admitted := <-admissions:
			if !admitted {
				t.Fatal("foreground demand was not admitted after preemption")
			}
		case <-time.After(time.Second):
			t.Fatal("foreground admission timed out")
		}
	}
	if got := canceled.Load(); got != extra {
		t.Fatalf("cancelled background leases = %d, want exactly %d", got, extra)
	}
	close(releaseForeground)
	foregroundWorkers.Wait()
	for _, release := range backgroundReleases {
		release()
	}
	backgroundWorkers.Wait()
}

func TestEncodePoolBackgroundNeedsMeasuredSpareCapacity(t *testing.T) {
	for name, capacity := range map[string]func() int{
		"software only": func() int { return 0 },
		"one slot":      func() int { return 1 },
		"unknown":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			p := NewEncodePool(capacity)
			if _, _, ok := p.AcquireBackground(t.Context(), time.Time{}); ok {
				t.Fatal("background work admitted without capacity beyond the live reserve")
			}
		})
	}
}

func TestEncodePoolReleaseIsIdempotentAndRaceSafe(t *testing.T) {
	const slots = 4
	p := NewEncodePool(func() int { return slots })
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if release, ok := p.AcquireForeground(context.Background()); ok {
				release()
				release()
			}
		}()
	}
	wg.Wait()

	releases := make([]func(), 0, slots)
	for range slots {
		release, ok := p.AcquireForeground(t.Context())
		if !ok {
			t.Fatal("an idempotent release leaked a slot")
		}
		releases = append(releases, release)
	}
	if _, ok := p.AcquireForeground(t.Context()); ok {
		t.Fatal("pool exceeded capacity after concurrent use")
	}
	for _, release := range releases {
		release()
	}
}
