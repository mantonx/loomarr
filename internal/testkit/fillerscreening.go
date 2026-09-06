package testkit

import (
	"context"
	"sync"

	"github.com/loomarr/loomarr/internal/filler"
)

// FillerScreeningService is a shared API-test fixture for the browser-safe
// screening summary seam. It records each requested clip identity so callers
// can assert that a summary was read for the expected catalog file.
type FillerScreeningService struct {
	Summary filler.SegmentScreeningSummary

	mu       sync.Mutex
	requests []FillerScreeningRequest
}

// FillerScreeningRequest identifies one screening summary request.
type FillerScreeningRequest struct {
	Hash string
	Path string
}

func (s *FillerScreeningService) ReadSegmentScreeningSummary(_ context.Context, hash, path string) (filler.SegmentScreeningSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, FillerScreeningRequest{Hash: hash, Path: path})
	return s.Summary, nil
}

// Requests returns a copy of the screening summary requests recorded so far.
func (s *FillerScreeningService) Requests() []FillerScreeningRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]FillerScreeningRequest(nil), s.requests...)
}
