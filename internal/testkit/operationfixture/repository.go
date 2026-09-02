// Package operationfixture provides cycle-free function-backed recordings for
// testing durable operations without importing an application package.
package operationfixture

import (
	"context"
	"sync"
)

// Repository records calls made through a durable operation's persistence
// interface and delegates results to typed callbacks supplied by the test.
type Repository[Run, Event, Reservation, Settlement any] struct {
	BeginFunc   func(context.Context, Run) (bool, error)
	AppendFunc  func(context.Context, Event) error
	ListFunc    func(context.Context, string) ([]Event, error)
	ReserveFunc func(context.Context, Reservation) (Event, error)
	SettleFunc  func(context.Context, Settlement) (Event, error)

	mu            sync.Mutex
	beginInputs   []Run
	appendInputs  []Event
	reserveInputs []Reservation
	settleInputs  []Settlement
}

func (r *Repository[Run, Event, Reservation, Settlement]) BeginSpokenSafetyRun(ctx context.Context, run Run) (bool, error) {
	r.mu.Lock()
	r.beginInputs = append(r.beginInputs, run)
	r.mu.Unlock()
	return r.BeginFunc(ctx, run)
}

func (r *Repository[Run, Event, Reservation, Settlement]) AppendSpokenSafetyEvent(ctx context.Context, event Event) error {
	r.mu.Lock()
	r.appendInputs = append(r.appendInputs, event)
	r.mu.Unlock()
	return r.AppendFunc(ctx, event)
}

func (r *Repository[Run, Event, Reservation, Settlement]) ListSpokenSafetyEvents(ctx context.Context, runID string) ([]Event, error) {
	return r.ListFunc(ctx, runID)
}

func (r *Repository[Run, Event, Reservation, Settlement]) ReserveSpokenSafetyCall(ctx context.Context, command Reservation) (Event, error) {
	r.mu.Lock()
	r.reserveInputs = append(r.reserveInputs, command)
	r.mu.Unlock()
	return r.ReserveFunc(ctx, command)
}

func (r *Repository[Run, Event, Reservation, Settlement]) SettleSpokenSafetyCall(ctx context.Context, command Settlement) (Event, error) {
	r.mu.Lock()
	r.settleInputs = append(r.settleInputs, command)
	r.mu.Unlock()
	return r.SettleFunc(ctx, command)
}

func (r *Repository[Run, Event, Reservation, Settlement]) Reservations() []Reservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Reservation(nil), r.reserveInputs...)
}

func (r *Repository[Run, Event, Reservation, Settlement]) Settlements() []Settlement {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Settlement(nil), r.settleInputs...)
}
