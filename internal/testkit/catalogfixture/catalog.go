// Package catalogfixture provides shared no-network adapters for catalog tests.
package catalogfixture

import (
	"context"
	"sync"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
)

// Corpus is a deterministic search/discovery corpus.
type Corpus struct {
	Candidates  []catalog.Candidate
	mu          sync.Mutex
	searches    []SearchRequest
	discoveries []DiscoveryRequest
}

type SearchRequest struct {
	Query string
	Limit int
}

type DiscoveryRequest struct {
	Query catalog.DiscoveryQuery
	Limit int
}

func (c *Corpus) Search(_ context.Context, query string, limit int) ([]catalog.Candidate, error) {
	c.mu.Lock()
	c.searches = append(c.searches, SearchRequest{Query: query, Limit: limit})
	c.mu.Unlock()
	return append([]catalog.Candidate(nil), c.Candidates...), nil
}

func (c *Corpus) Discover(_ context.Context, query catalog.DiscoveryQuery, limit int) ([]catalog.Candidate, error) {
	c.mu.Lock()
	c.discoveries = append(c.discoveries, DiscoveryRequest{Query: query, Limit: limit})
	c.mu.Unlock()
	return append([]catalog.Candidate(nil), c.Candidates...), nil
}

func (c *Corpus) Searches() []SearchRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SearchRequest(nil), c.searches...)
}

func (c *Corpus) Discoveries() []DiscoveryRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]DiscoveryRequest(nil), c.discoveries...)
}

// Presence is a deterministic library-ownership adapter.
type Presence struct {
	mu    sync.Mutex
	Hits  map[int]catalog.Presence
	calls []int
}

func (p *Presence) Present(
	_ context.Context,
	_ provision.MediaType,
	tmdbID, _ int,
) (catalog.Presence, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, tmdbID)
	hit, ok := p.Hits[tmdbID]
	return hit, ok, nil
}

func (p *Presence) Calls() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.calls...)
}
