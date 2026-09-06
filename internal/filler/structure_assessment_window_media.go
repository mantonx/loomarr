package filler

import (
	"context"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

// StructureAssessmentWindowMedia pairs one path-free window authority with its machine-local
// retained location. Paths are transport inputs only and never enter decision identity.
type StructureAssessmentWindowMedia struct {
	Window   fillerstructurewindow.Window
	Media    fillerstructurewindow.WindowMedia
	FullPath string
}

// StructureAssessmentWindowMediaSet is the complete prepared input for one long-reel plan.
type StructureAssessmentWindowMediaSet struct {
	Source    SplitSourceAsset
	Authority fillerstructurewindow.MediaSet
	Windows   []StructureAssessmentWindowMedia
}

// StructureAssessmentWindowMediaPreparer returns either every verified window or no usable set.
type StructureAssessmentWindowMediaPreparer interface {
	PrepareWindows(context.Context, StructureAssessmentSource, fillerstructurewindow.Plan) (StructureAssessmentWindowMediaSet, error)
}

var _ StructureAssessmentWindowMediaPreparer = (*FFmpegStructureAssessmentMediaPreparer)(nil)
