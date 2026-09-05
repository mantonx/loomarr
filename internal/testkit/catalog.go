package testkit

import (
	"context"
	"sync"
)

// SearchService is the shared in-memory double for a typed catalog search seam.
// Generic request and result types keep testkit independent of the package that
// owns the public search DTOs while still satisfying its Search method structurally.
type SearchService[Q, T any] struct {
	mu       sync.Mutex
	Results  []T
	Err      error
	requests []Q
}

func (s *SearchService[Q, T]) Search(_ context.Context, request Q) ([]T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	return append([]T(nil), s.Results...), s.Err
}

func (s *SearchService[Q, T]) Requests() []Q {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Q(nil), s.requests...)
}

// IconService is the shared in-memory double for a typed channel-icon suggestion
// seam. Like SearchService, its generic result avoids coupling testkit to the API
// package's presentation type.
type IconService[T any] struct {
	mu         sync.Mutex
	Results    []T
	Err        error
	channelIDs []string
}

func (s *IconService[T]) IconSuggestions(_ context.Context, channelID string) ([]T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelIDs = append(s.channelIDs, channelID)
	return append([]T(nil), s.Results...), s.Err
}

func (s *IconService[T]) ChannelIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.channelIDs...)
}
