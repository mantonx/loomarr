package filler

import (
	"context"
	"fmt"
	"slices"

	"github.com/loomarr/loomarr/internal/fillerairworthinessprojection"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/mediatools"
)

// SpokenSafetyProducerRequest gives the execution adapter one stable operation identity and the
// already verified private evidence locator. The path is never part of returned authority.
type SpokenSafetyProducerRequest struct {
	OperationSHA256 string
	Subject         fillerairworthinessprojection.Subject
	EvidencePath    string
	EvidenceTool    mediatools.MediaToolIdentity
}

// SpokenSafetyProducer is the execution seam. A production adapter must durably replay the same
// OperationSHA256 after completing a call, including when screening persistence later fails.
type SpokenSafetyProducer interface {
	EvaluateSpokenSafety(context.Context, SpokenSafetyProducerRequest) (fillersafety.EvaluationReport, error)
}

// SpokenSafetyEvaluator authenticates a producer report through one immutable projection
// authority. The producer supplies evidence; this adapter and Airworthiness retain policy control.
type SpokenSafetyEvaluator struct {
	projected *projectedSafetyEvaluator
	authority fillerairworthinessprojection.SpokenAuthority
	producer  SpokenSafetyProducer
}

func NewSpokenSafetyEvaluator(authority fillerairworthinessprojection.SpokenAuthority, replay SegmentScreeningAxisEvidenceReplay, producer SpokenSafetyProducer) (*SpokenSafetyEvaluator, error) {
	profile, err := fillerairworthinessprojection.SpokenProfile(authority)
	if err != nil || producer == nil {
		return nil, fmt.Errorf("spoken safety evaluator authority or producer is invalid")
	}
	projected, err := newProjectedSafetyEvaluator(ScreenSpokenSafety, profile, replay)
	if err != nil {
		return nil, err
	}
	authority.Rules = slices.Clone(authority.Rules)
	return &SpokenSafetyEvaluator{projected: projected, authority: authority, producer: producer}, nil
}

func (e *SpokenSafetyEvaluator) Axis() SegmentScreeningAxis { return ScreenSpokenSafety }

func (e *SpokenSafetyEvaluator) Evaluate(ctx context.Context, media SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, error) {
	if e == nil || e.projected == nil || e.producer == nil ||
		fillerairworthinessprojection.ValidateSpokenAuthority(e.authority) != nil || e.projected.profile.Axis != ScreenSpokenSafety {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("spoken safety evaluator is unavailable")
	}
	replayed, found, operationSHA256, err := e.projected.begin(ctx, media)
	if err != nil || found {
		return replayed, err
	}
	subject := projectedSafetySubject(media.Subject)
	report, err := e.producer.EvaluateSpokenSafety(ctx, SpokenSafetyProducerRequest{
		OperationSHA256: operationSHA256, Subject: subject, EvidencePath: media.EvidencePath,
		EvidenceTool: media.Manifest.Evidence.Tool,
	})
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("produce spoken safety evidence: %w", err)
	}
	if report.Run.ID != operationSHA256 {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("spoken safety producer returned a different operation")
	}
	projection, err := fillerairworthinessprojection.ProjectSpoken(subject, report, e.authority)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("project spoken safety evidence: %w", err)
	}
	return e.projected.settle(media.Subject, projection.Evidence, projection.RawEvidence, report.TerminalCreatedAt)
}

func projectedSafetySubject(subject SegmentScreeningSubject) fillerairworthinessprojection.Subject {
	return fillerairworthinessprojection.Subject{
		SHA256: subject.SHA256, EvidenceSHA256: subject.EvidenceSHA256,
		EvidenceBytes: subject.EvidenceBytes, DurationMS: subject.EvidenceDurationMs,
	}
}

var _ SegmentScreeningEvaluator = (*SpokenSafetyEvaluator)(nil)
