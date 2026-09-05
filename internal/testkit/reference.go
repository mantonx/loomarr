package testkit

import (
	"context"
	"sync"

	"github.com/loomarr/loomarr/internal/reference"
)

// ReferenceResolver is the shared no-network double for reference-backed Intent
// tests. Calls returns copied lookups so assertions do not race background jobs.
type ReferenceResolver struct {
	mu       sync.Mutex
	Evidence reference.Evidence
	Err      error
	lookups  []reference.Lookup
}

func (r *ReferenceResolver) Lookup(_ context.Context, lookup reference.Lookup) (reference.Evidence, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups = append(r.lookups, lookup)
	return r.Evidence, r.Err
}

func (r *ReferenceResolver) Calls() []reference.Lookup {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]reference.Lookup(nil), r.lookups...)
}
