package filler

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthinessprojection"
	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

// VisualSafetyProducerRequest gives the visual execution adapter one stable operation identity
// and the already verified evidence locator. The path is excluded from all returned evidence.
type VisualSafetyProducerRequest struct {
	OperationSHA256 string
	Subject         fillerairworthinessprojection.Subject
	EvidencePath    string
}

// VisualSafetyProducerEvidence is the complete path-free input required to reproduce the visual
// reducer and project only certified opaque matches.
type VisualSafetyProducerEvidence struct {
	OperationSHA256 string
	Source          fillervisualsafety.SourceAuthority
	Plan            fillervisualsafety.CoveragePlan
	Coverage        fillervisualsafety.CoverageEvidence
	Observations    []fillervisualsafety.Observation
	Result          fillervisualsafety.Result
}

// VisualSafetyProducer is the execution seam. A production adapter must durably replay the same
// OperationSHA256 after completing any local or hosted inference work.
type VisualSafetyProducer interface {
	EvaluateVisualSafety(context.Context, VisualSafetyProducerRequest) (VisualSafetyProducerEvidence, error)
}

// VisualSafetyEvaluator authenticates a complete visual producer result through one immutable
// projection authority, without allowing producer prose or confidence to decide admission.
type VisualSafetyEvaluator struct {
	projected *projectedSafetyEvaluator
	authority fillerairworthinessprojection.VisualAuthority
	producer  VisualSafetyProducer
}

func NewVisualSafetyEvaluator(authority fillerairworthinessprojection.VisualAuthority, replay SegmentScreeningAxisEvidenceReplay, producer VisualSafetyProducer) (*VisualSafetyEvaluator, error) {
	profile, err := fillerairworthinessprojection.VisualProfile(authority)
	if err != nil || producer == nil {
		return nil, fmt.Errorf("visual safety evaluator authority or producer is invalid")
	}
	projected, err := newProjectedSafetyEvaluator(ScreenVisualSafety, profile, replay)
	if err != nil {
		return nil, err
	}
	authority.Producers = slices.Clone(authority.Producers)
	authority.Rules = slices.Clone(authority.Rules)
	return &VisualSafetyEvaluator{projected: projected, authority: authority, producer: producer}, nil
}

func (e *VisualSafetyEvaluator) Axis() SegmentScreeningAxis { return ScreenVisualSafety }

func (e *VisualSafetyEvaluator) Evaluate(ctx context.Context, media SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, error) {
	if e == nil || e.projected == nil || e.producer == nil ||
		fillerairworthinessprojection.ValidateVisualAuthority(e.authority) != nil || e.projected.profile.Axis != ScreenVisualSafety {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("visual safety evaluator is unavailable")
	}
	replayed, found, operationSHA256, err := e.projected.begin(ctx, media)
	if err != nil || found {
		return replayed, err
	}
	subject := projectedSafetySubject(media.Subject)
	produced, err := e.producer.EvaluateVisualSafety(ctx, VisualSafetyProducerRequest{
		OperationSHA256: operationSHA256, Subject: subject, EvidencePath: media.EvidencePath,
	})
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("produce visual safety evidence: %w", err)
	}
	if produced.OperationSHA256 != operationSHA256 {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("visual safety producer returned a different operation")
	}
	projection, err := fillerairworthinessprojection.ProjectVisual(
		subject, produced.Source, produced.Plan, produced.Coverage, produced.Observations, produced.Result, e.authority,
	)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("project visual safety evidence: %w", err)
	}
	return e.projected.settle(media.Subject, projection.Evidence, projection.RawEvidence, visualSafetyAssessedAt(produced))
}

func visualSafetyAssessedAt(evidence VisualSafetyProducerEvidence) time.Time {
	latest := evidence.Source.MeasuredAt
	for _, observation := range evidence.Observations {
		if observation.AssessedAt.After(latest) {
			latest = observation.AssessedAt
		}
	}
	return latest
}

var _ SegmentScreeningEvaluator = (*VisualSafetyEvaluator)(nil)
