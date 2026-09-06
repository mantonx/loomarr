// Package operationfixture provides cycle-free function-backed recordings for
// testing durable operations without importing an application package.
package operationfixture

import (
	"context"
	"reflect"
	"slices"
	"sync"
)

// State is the reusable durable-state model for an operation fixture. It owns
// the immutable run header and append-only event history.
type State[Run, Event any] struct {
	mu     sync.Mutex
	run    Run
	begun  bool
	events []Event
}

func (s *State[Run, Event]) begin(run Run, conflict error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.begun {
		if !reflect.DeepEqual(s.run, run) {
			return false, conflict
		}
		return false, nil
	}
	s.run, s.begun = run, true
	return true, nil
}

// Begun reports whether the fixture has durably accepted its run header.
func (s *State[Run, Event]) Begun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.begun
}

// Events returns an ordered snapshot of durably appended events.
func (s *State[Run, Event]) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func (s *State[Run, Event]) append(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

// Repository records calls made through a durable operation's persistence
// interface. Domain-specific validation and typed event construction remain
// callbacks; State owns the generic persistence and idempotency mechanics.
type Repository[Run, Event, Reservation, Settlement any] struct {
	State         *State[Run, Event]
	ValidateRun   func(Run) error
	ValidateEvent func(Event) error
	ConflictError error
	ReserveFunc   func(context.Context, Reservation) (Event, error)
	SettleFunc    func(context.Context, Settlement) (Event, error)

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
	if err := r.ValidateRun(run); err != nil {
		return false, err
	}
	return r.State.begin(run, r.ConflictError)
}

func (r *Repository[Run, Event, Reservation, Settlement]) AppendSpokenSafetyEvent(ctx context.Context, event Event) error {
	r.mu.Lock()
	r.appendInputs = append(r.appendInputs, event)
	r.mu.Unlock()
	if err := r.ValidateEvent(event); err != nil {
		return err
	}
	r.State.append(event)
	return nil
}

func (r *Repository[Run, Event, Reservation, Settlement]) ListSpokenSafetyEvents(ctx context.Context, runID string) ([]Event, error) {
	return r.State.Events(), nil
}

func (r *Repository[Run, Event, Reservation, Settlement]) ReserveSpokenSafetyCall(ctx context.Context, command Reservation) (Event, error) {
	r.mu.Lock()
	r.reserveInputs = append(r.reserveInputs, command)
	r.mu.Unlock()
	event, err := r.ReserveFunc(ctx, command)
	if err != nil {
		return event, err
	}
	if err := r.AppendSpokenSafetyEvent(ctx, event); err != nil {
		return event, err
	}
	return event, nil
}

func (r *Repository[Run, Event, Reservation, Settlement]) SettleSpokenSafetyCall(ctx context.Context, command Settlement) (Event, error) {
	r.mu.Lock()
	r.settleInputs = append(r.settleInputs, command)
	r.mu.Unlock()
	event, err := r.SettleFunc(ctx, command)
	if err != nil {
		return event, err
	}
	if err := r.AppendSpokenSafetyEvent(ctx, event); err != nil {
		return event, err
	}
	return event, nil
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

// Begun and Events expose durable-state snapshots for fixture assertions.
func (r *Repository[Run, Event, Reservation, Settlement]) Begun() bool { return r.State.Begun() }

func (r *Repository[Run, Event, Reservation, Settlement]) Events() []Event { return r.State.Events() }
