package filler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	qualificationSafetyEvidenceSchemaVersion   = 1
	qualificationSafetyEvidenceContractVersion = "filler-rendered-child-safety-qualification-evidence-v1"
	qualificationSafetyImplementationVersion   = "filler-rendered-child-safety-qualification-evaluator-v1"
	qualificationRightsImplementationVersion   = "filler-current-broadcast-rights-evaluator-v1"
	qualificationPlaybackImplementationVersion = "filler-playback-integrity-evaluator-v1"
)

type qualificationSafetyRawEvidence struct {
	SchemaVersion           int                                 `json:"schemaVersion"`
	ContractVersion         string                              `json:"contractVersion"`
	SubjectSHA256           string                              `json:"subjectSha256"`
	Axis                    SegmentScreeningAxis                `json:"axis"`
	Evidence                segmentScreeningArtifactObservation `json:"evidence"`
	EvidenceIdentityMatched bool                                `json:"evidenceIdentityMatched"`
	Outcome                 SegmentScreeningOutcome             `json:"outcome"`
	ReasonCode              string                              `json:"reasonCode"`
}

// qualificationSafetyEvaluator makes an unavailable safety authority explicit and durable. It
// proves which evidence bytes reached the boundary, but it cannot infer safety or produce a pass.
// A certified evaluator replaces the entire adapter for its axis; this is not a permissive fallback.
type qualificationSafetyEvaluator struct {
	profile SegmentScreeningAxisProfile
	replay  SegmentScreeningAxisEvidenceReplay
	now     func() time.Time
}

func newQualificationSafetyEvaluator(axis SegmentScreeningAxis, replay SegmentScreeningAxisEvidenceReplay, now func() time.Time) (*qualificationSafetyEvaluator, error) {
	if axis != ScreenVisualSafety && axis != ScreenSpokenSafety && axis != ScreenWrittenSafety {
		return nil, fmt.Errorf("qualification safety axis is invalid")
	}
	if replay == nil || now == nil {
		return nil, fmt.Errorf("qualification safety evaluator requires replay and clock")
	}
	profile := qualificationSegmentScreeningProfile(axis, qualificationSafetyEvidenceContractVersion, qualificationSafetyImplementationVersion)
	if err := ValidateSegmentScreeningAxisProfile(profile); err != nil {
		return nil, err
	}
	return &qualificationSafetyEvaluator{profile: profile, replay: replay, now: now}, nil
}

func (e *qualificationSafetyEvaluator) Axis() SegmentScreeningAxis {
	if e == nil {
		return ""
	}
	return e.profile.Axis
}

func (e *qualificationSafetyEvaluator) Evaluate(ctx context.Context, media SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, error) {
	if e == nil || e.replay == nil || e.now == nil || ValidateSegmentScreeningAxisProfile(e.profile) != nil ||
		(e.profile.Axis != ScreenVisualSafety && e.profile.Axis != ScreenSpokenSafety && e.profile.Axis != ScreenWrittenSafety) ||
		e.profile.EvidenceContract != qualificationSafetyEvidenceContractVersion {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("qualification safety evaluator is unavailable")
	}
	if err := validateSegmentScreeningMedia(media); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	observation, matches, err := inspectSegmentScreeningArtifact(
		ctx, media.EvidencePath, media.Subject.EvidenceSHA256, media.Subject.EvidenceBytes, media.Subject.EvidenceHash,
	)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	reasonCode := string(e.profile.Axis) + "_not_certified"
	raw, err := json.Marshal(qualificationSafetyRawEvidence{
		SchemaVersion: qualificationSafetyEvidenceSchemaVersion, ContractVersion: qualificationSafetyEvidenceContractVersion,
		SubjectSHA256: media.Subject.SHA256, Axis: e.profile.Axis, Evidence: observation,
		EvidenceIdentityMatched: matches, Outcome: ScreenHold, ReasonCode: reasonCode,
	})
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("marshal qualification safety evidence: %w", err)
	}
	replayed, found, err := e.replay.FindSegmentScreeningAxisEvidence(ctx, media.Subject.SHA256, e.profile)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("replay qualification safety evidence: %w", err)
	}
	if found {
		if replayed.Evidence.Outcome != ScreenHold || replayed.Evidence.ReasonCode != reasonCode || !bytes.Equal(replayed.RawEvidence, raw) {
			return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("qualification safety operation conflicts with its settled result")
		}
		return replayed, nil
	}
	return NewSegmentScreeningAxisEvidence(media.Subject, e.profile, ScreenHold, reasonCode, raw, e.now())
}

// NewQualificationSegmentScreeningRuntime activates the complete five-axis production boundary
// before the three inference-backed safety lanes are certified. The local rights and playback
// axes run normally; each missing safety axis records a non-authorizing hold over the exact child.
func NewQualificationSegmentScreeningRuntime(evidenceRoot string, rightsRepository FillerRightsGrantRepository, now func() time.Time) (*SegmentScreeningRuntime, error) {
	if rightsRepository == nil || now == nil {
		return nil, fmt.Errorf("qualification segment screening requires rights repository and clock")
	}
	evidence, err := NewFileSegmentScreeningEvidenceRepository(evidenceRoot)
	if err != nil {
		return nil, fmt.Errorf("qualification segment screening evidence: %w", err)
	}
	rights, err := NewFillerRightsRegistry(rightsRepository)
	if err != nil {
		return nil, err
	}
	evaluators := make([]SegmentScreeningEvaluator, 0, len(segmentScreeningAxisOrder))
	for _, axis := range []SegmentScreeningAxis{ScreenVisualSafety, ScreenSpokenSafety, ScreenWrittenSafety} {
		evaluator, evaluatorErr := newQualificationSafetyEvaluator(axis, evidence, now)
		if evaluatorErr != nil {
			return nil, evaluatorErr
		}
		evaluators = append(evaluators, evaluator)
	}
	rightsEvaluator, err := NewFillerRightsEvaluator(
		qualificationSegmentScreeningProfile(ScreenRights, fillerRightsEvidenceContractVersion, qualificationRightsImplementationVersion),
		evidence, rights, now,
	)
	if err != nil {
		return nil, err
	}
	playbackEvaluator, err := NewPlaybackIntegrityEvaluator(
		qualificationSegmentScreeningProfile(ScreenPlayback, playbackIntegrityEvidenceContractVersion, qualificationPlaybackImplementationVersion),
		evidence, now,
	)
	if err != nil {
		return nil, err
	}
	evaluators = append(evaluators, rightsEvaluator, playbackEvaluator)
	return NewSegmentScreeningRuntime(evaluators, evidence)
}

// qualificationSegmentScreeningProfile hashes explicit built-in, non-authorizing identity
// documents. In particular the certification hash means "unavailable"; it cannot be confused
// with or accepted by a later production release authority.
func qualificationSegmentScreeningProfile(axis SegmentScreeningAxis, evidenceContract, implementationVersion string) SegmentScreeningAxisProfile {
	return SegmentScreeningAxisProfile{
		Axis:                 axis,
		EvidenceContract:     evidenceContract,
		PolicySHA256:         screeningBytesSHA256([]byte("filler-segment-screening-qualification-policy-v1\x00" + string(axis))),
		CertificationSHA256:  screeningBytesSHA256([]byte("filler-segment-screening-certification-unavailable-v1\x00" + string(axis))),
		ImplementationSHA256: screeningBytesSHA256([]byte(implementationVersion + "\x00" + string(axis))),
	}
}

var _ SegmentScreeningEvaluator = (*qualificationSafetyEvaluator)(nil)
