package app

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

// buildQualificationSegmentScreeningRuntime keeps the rendered-child screening composition out
// of the already broad pipeline builder. A configured filler root gets one private evidence tree;
// an installation without filler storage retains the stage's ordinary unavailable hold.
func buildQualificationSegmentScreeningRuntime(st store.Store, layout filler.Layout) (*filler.SegmentScreeningRuntime, error) {
	if layout.ClipDir() == "" {
		return nil, nil
	}
	if st == nil {
		return nil, fmt.Errorf("qualification segment screening requires a store")
	}
	root := segmentScreeningEvidenceRoot(layout)
	return filler.NewQualificationSegmentScreeningRuntime(root, st, time.Now)
}

func buildSegmentScreeningSummaryService(layout filler.Layout) (*filler.SegmentScreeningSummaryService, error) {
	if layout.ClipDir() == "" {
		return nil, nil
	}
	repository, err := filler.NewFileSegmentScreeningEvidenceRepository(segmentScreeningEvidenceRoot(layout))
	if err != nil {
		return nil, fmt.Errorf("build segment screening summary evidence: %w", err)
	}
	service, err := filler.NewSegmentScreeningSummaryService(repository)
	if err != nil {
		return nil, fmt.Errorf("build segment screening summary service: %w", err)
	}
	return service, nil
}

func segmentScreeningEvidenceRoot(layout filler.Layout) string {
	return filepath.Join(layout.ClipDir(), ".loomarr", "segment-screening")
}
