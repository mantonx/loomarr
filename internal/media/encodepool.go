// Package media owns host-wide resources shared by live and background media work.
package media

import (
	"context"
	"sync"
	"time"
)

const foregroundPreemptionWait = 500 * time.Millisecond

// EncodePool is the single admission boundary for hardware video encodes. Foreground playback may
// use every measured slot. Background preparation may fill measured capacity minus one, leaving a
// live reserve; each lease receives a cancelled context when foreground demand needs its slot.
type EncodePool struct {
	capacityFn func() int

	once     sync.Once
	capacity int // -1 means unmeasured: foreground is unbounded, background is disabled.

	mu          sync.Mutex
	held        int
	waiters     int
	nextID      uint64
	backgrounds map[uint64]*backgroundLease
	changed     chan struct{}
}

type backgroundLease struct {
	id         uint64
	neededAt   time.Time
	cancel     context.CancelFunc
	preempting bool
}

// NewEncodePool creates a host-wide hardware encode pool. capacity is resolved once because the
// underlying encoder probe is a property of the running process and can be expensive.
func NewEncodePool(capacity func() int) *EncodePool {
	return &EncodePool{
		capacityFn: capacity, backgrounds: make(map[uint64]*backgroundLease), changed: make(chan struct{}),
	}
}

func (p *EncodePool) init() {
	p.once.Do(func() {
		p.capacity = -1
		if p.capacityFn != nil {
			p.capacity = p.capacityFn()
		}
	})
}

// AcquireForeground takes a hardware slot for live playback. When preparation owns the last slot,
// its context is cancelled and playback waits briefly for the process to release it; a stuck
// background process therefore degrades this caller to software rather than delaying tune-in.
func (p *EncodePool) AcquireForeground(ctx context.Context) (release func(), ok bool) {
	if p == nil {
		return func() {}, true
	}
	p.init()

	timer := time.NewTimer(foregroundPreemptionWait)
	defer timer.Stop()
	waiting := false
	for {
		p.mu.Lock()
		if p.capacity < 0 {
			p.mu.Unlock()
			return func() {}, true
		}
		if p.held < p.capacity {
			if waiting {
				p.waiters--
			}
			release = p.acquireLocked(0, nil)
			p.mu.Unlock()
			return release, true
		}
		if len(p.backgrounds) == 0 {
			if waiting {
				p.waiters--
			}
			p.mu.Unlock()
			return nil, false
		}
		if !waiting {
			p.waiters++
			waiting = true
		}
		var victim *backgroundLease
		preempting := 0
		for _, candidate := range p.backgrounds {
			if candidate.preempting {
				preempting++
				continue
			}
			if victim == nil || candidate.neededAt.After(victim.neededAt) ||
				(candidate.neededAt.Equal(victim.neededAt) && candidate.id > victim.id) {
				victim = candidate
			}
		}
		if victim != nil && p.waiters > preempting {
			victim.preempting = true
			victim.cancel()
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			p.removeWaiter()
			return nil, false
		case <-timer.C:
			p.removeWaiter()
			return nil, false
		}
	}
}

// AcquireBackground takes one preparation slot only when measured hardware capacity leaves a
// separate foreground reserve. neededAt lets foreground preempt the least urgent work. The returned
// context, not the caller's original context, must be passed to the encoder.
func (p *EncodePool) AcquireBackground(ctx context.Context, neededAt time.Time) (
	workCtx context.Context, release func(), ok bool,
) {
	if p == nil {
		return nil, nil, false
	}
	p.init()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.capacity < 2 || p.held >= p.capacity-1 {
		return nil, nil, false
	}
	workCtx, cancel := context.WithCancel(ctx)
	p.nextID++
	id := p.nextID
	p.backgrounds[id] = &backgroundLease{id: id, neededAt: neededAt, cancel: cancel}
	return workCtx, p.acquireLocked(id, cancel), true
}

func (p *EncodePool) acquireLocked(backgroundID uint64, cancel context.CancelFunc) func() {
	p.held++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			p.held--
			if backgroundID != 0 {
				delete(p.backgrounds, backgroundID)
				cancel()
			}
			p.signalLocked()
			p.mu.Unlock()
		})
	}
}

func (p *EncodePool) removeWaiter() {
	p.mu.Lock()
	p.waiters--
	p.signalLocked()
	p.mu.Unlock()
}

func (p *EncodePool) signalLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}
