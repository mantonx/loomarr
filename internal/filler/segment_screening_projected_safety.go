package filler

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
)

// projectedSafetyEvaluator owns the common safety-axis operation: prove the current rendered
// child, replay a settled result before producer work, and bind projected evidence to screening.
// Producer-specific execution and authentication stay in the two concrete adapters.
type projectedSafetyEvaluator struct {
	profile SegmentScreeningAxisProfile
	replay  SegmentScreeningAxisEvidenceReplay
}

func newProjectedSafetyEvaluator(axis SegmentScreeningAxis, profile fillerairworthiness.AxisProfile, replay SegmentScreeningAxisEvidenceReplay) (*projectedSafetyEvaluator, error) {
	wantAxis, safety := airworthinessAxis(axis)
	candidate := SegmentScreeningAxisProfile{
		Axis: axis, EvidenceContract: profile.EvidenceContract,
		PolicySHA256: profile.PolicySHA256, CertificationSHA256: profile.CertificationSHA256,
		ImplementationSHA256:      profile.ImplementationSHA256,
		CertifiedSuitabilityFlags: append([]fillerairworthiness.Flag(nil), profile.CertifiedFlags...),
	}
	if !safety || profile.Axis != wantAxis || replay == nil || ValidateSegmentScreeningAxisProfile(candidate) != nil {
		return nil, fmt.Errorf("projected safety evaluator profile is invalid")
	}
	return &projectedSafetyEvaluator{profile: candidate, replay: replay}, nil
}

func (e *projectedSafetyEvaluator) begin(ctx context.Context, media SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, bool, string, error) {
	if e == nil || e.replay == nil || ValidateSegmentScreeningAxisProfile(e.profile) != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, "", fmt.Errorf("projected safety evaluator is unavailable")
	}
	if err := validateSegmentScreeningMedia(media); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, "", err
	}
	observation, matches, err := inspectSegmentScreeningArtifact(
		ctx, media.EvidencePath, media.Subject.EvidenceSHA256, media.Subject.EvidenceBytes, media.Subject.EvidenceHash,
	)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, "", err
	}
	if !matches {
		return RecordedSegmentScreeningAxisEvidence{}, false, "", fmt.Errorf("%s evidence derivative does not match its subject (%s)", e.profile.Axis, observation.State)
	}
	replayed, found, err := e.replay.FindSegmentScreeningAxisEvidence(ctx, media.Subject.SHA256, e.profile)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, "", fmt.Errorf("replay %s evidence: %w", e.profile.Axis, err)
	}
	if found {
		if ValidateRecordedSegmentScreeningAxisEvidence(replayed) != nil || replayed.Evidence.SubjectSHA256 != media.Subject.SHA256 ||
			!reflect.DeepEqual(replayed.Evidence.Profile, e.profile) {
			return RecordedSegmentScreeningAxisEvidence{}, false, "", fmt.Errorf("replayed %s evidence drifted", e.profile.Axis)
		}
		return replayed, true, "", nil
	}
	return RecordedSegmentScreeningAxisEvidence{}, false, segmentScreeningOperationSHA256(media.Subject.SHA256, e.profile), nil
}

func (e *projectedSafetyEvaluator) settle(subject SegmentScreeningSubject, evidence fillerairworthiness.AxisEvidence, raw []byte, assessedAt time.Time) (RecordedSegmentScreeningAxisEvidence, error) {
	profile, err := segmentAirworthinessProfile(e.profile)
	if err != nil || evidence.SubjectSHA256 != subject.SHA256 || !reflect.DeepEqual(evidence.Profile, profile) ||
		evidence.EvidenceSHA256 != screeningBytesSHA256(raw) || fillerairworthiness.ValidateAxisEvidence(evidence, subject.EvidenceDurationMs) != nil ||
		len(raw) == 0 || len(raw) > segmentScreeningAxisRawMaxBytes || assessedAt.IsZero() {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("%s projected evidence is invalid or cannot be persisted", e.profile.Axis)
	}
	outcome, reasonCode := ScreenHold, string(e.profile.Axis)+"_evidence_incomplete"
	if evidence.Coverage == fillerairworthiness.CoverageComplete {
		outcome, reasonCode = ScreenPass, string(e.profile.Axis)+"_evidence_complete"
	} else if evidence.Coverage != fillerairworthiness.CoverageIncomplete {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("%s projected evidence has unsupported coverage", e.profile.Axis)
	}
	return NewSegmentScreeningAxisEvidence(subject, e.profile, outcome, reasonCode, &evidence, raw, assessedAt)
}
