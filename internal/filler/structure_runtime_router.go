package filler

import (
	"context"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

// StructureAssessmentRuntimeRouter selects one certified representation from immutable source
// duration before either implementation performs preparation or inference. It never falls back
// after selection because that would change the evidence and accounting operation.
type StructureAssessmentRuntimeRouter struct {
	completeVideo      CompleteTimelineStructureDecisioner
	windowed           CompleteTimelineStructureDecisioner
	completeVideoMaxMS int64
	windowedMaxMS      int64
}

func NewStructureAssessmentRuntimeRouter(completeVideo, windowed CompleteTimelineStructureDecisioner, completeVideoMaxMS int64) (*StructureAssessmentRuntimeRouter, error) {
	if completeVideo == nil || windowed == nil || completeVideoMaxMS <= 0 ||
		completeVideoMaxMS >= fillerstructurewindow.MaximumSourceDurationMS {
		return nil, errors.New("structure assessment router requires distinct complete-video and window duration envelopes")
	}
	return &StructureAssessmentRuntimeRouter{
		completeVideo: completeVideo, windowed: windowed, completeVideoMaxMS: completeVideoMaxMS,
		windowedMaxMS: fillerstructurewindow.MaximumSourceDurationMS,
	}, nil
}

func (r *StructureAssessmentRuntimeRouter) Assess(ctx context.Context, input StructureAssessmentSource) (fillerstructure.Artifact, error) {
	if r == nil || r.completeVideo == nil || r.windowed == nil || r.completeVideoMaxMS <= 0 ||
		r.windowedMaxMS != fillerstructurewindow.MaximumSourceDurationMS || r.completeVideoMaxMS >= r.windowedMaxMS {
		return fillerstructure.Artifact{}, errors.New("structure assessment router is unavailable")
	}
	if err := input.Source.validate(); err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("route structure assessment: %w", err)
	}
	switch {
	case input.Source.DurationMs <= r.completeVideoMaxMS:
		return r.completeVideo.Assess(ctx, input)
	case input.Source.DurationMs <= r.windowedMaxMS:
		return r.windowed.Assess(ctx, input)
	default:
		return fillerstructure.Artifact{}, fmt.Errorf("route structure assessment: source duration %d exceeds protocol capacity %d", input.Source.DurationMs, r.windowedMaxMS)
	}
}

var _ CompleteTimelineStructureDecisioner = (*StructureAssessmentRuntimeRouter)(nil)
