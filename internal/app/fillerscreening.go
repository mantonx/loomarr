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
	root := filepath.Join(layout.ClipDir(), ".loomarr", "segment-screening")
	return filler.NewQualificationSegmentScreeningRuntime(root, st, time.Now)
}
