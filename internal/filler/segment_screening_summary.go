package filler

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
)

type SegmentScreeningSummaryState string

const (
	ScreeningSummaryNotScreened SegmentScreeningSummaryState = "not_screened"
	ScreeningSummaryAvailable   SegmentScreeningSummaryState = "available"
	ScreeningSummaryUnavailable SegmentScreeningSummaryState = "unavailable"
)

const (
	ScreeningSummaryReasonNotAttached         = "screening_not_attached"
	ScreeningSummaryReasonSidecarInvalid      = "screening_sidecar_invalid"
	ScreeningSummaryReasonEvidenceUnavailable = "screening_evidence_unavailable"
	ScreeningSummaryReasonEvidenceDrift       = "screening_evidence_drift"
)

// SegmentScreeningAxisSummary is the browser-safe closed result for one independent screen.
// EvidenceSHA256 names the immutable axis record; it is not raw provider evidence.
type SegmentScreeningAxisSummary struct {
	Axis           SegmentScreeningAxis
	Outcome        SegmentScreeningOutcome
	ReasonCode     string
	EvidenceSHA256 string
}

// SegmentScreeningSummary is the public-data-minimized projection for one exact catalog clip.
// Airworthiness is already a path-free closed public decision; raw axis evidence stays private.
type SegmentScreeningSummary struct {
	State          SegmentScreeningSummaryState
	ReasonCode     string
	ClipHash       string
	SubjectSHA256  string
	EvidenceSHA256 string
	Outcome        SegmentScreeningOutcome
	Axes           []SegmentScreeningAxisSummary
	RightsScope    *FillerRightsScope
	Airworthiness  *fillerairworthiness.Decision
	AssessedAt     time.Time
}

// SegmentScreeningSummaryEvidenceReader is the narrow immutable evidence seam needed by the
// operator projection. It cannot read raw axis evidence.
type SegmentScreeningSummaryEvidenceReader interface {
	GetSegmentScreeningSubject(context.Context, string) (SegmentScreeningSubject, error)
	GetSegmentScreeningEvidence(context.Context, string) (SegmentScreeningEvidence, error)
	GetSegmentScreeningAxisRecord(context.Context, string) (SegmentScreeningAxisEvidence, error)
}

type segmentScreeningSummaryArtifactInspector func(
	context.Context, string, string, int64, string,
) (segmentScreeningArtifactObservation, bool, error)

// SegmentScreeningSummaryService reproduces a sidecar reference against immutable evidence and
// publishes only the closed values suitable for the browser.
type SegmentScreeningSummaryService struct {
	evidence SegmentScreeningSummaryEvidenceReader
	inspect  segmentScreeningSummaryArtifactInspector
}

func NewSegmentScreeningSummaryService(evidence SegmentScreeningSummaryEvidenceReader) (*SegmentScreeningSummaryService, error) {
	if evidence == nil {
		return nil, fmt.Errorf("segment screening summary requires evidence")
	}
	return &SegmentScreeningSummaryService{evidence: evidence, inspect: inspectSegmentScreeningArtifact}, nil
}

func (s *SegmentScreeningSummaryService) ReadSegmentScreeningSummary(ctx context.Context, clipHash, mediaPath string) (SegmentScreeningSummary, error) {
	if s == nil || s.evidence == nil || s.inspect == nil || ctx == nil || ctx.Err() != nil || !isContentHash(clipHash) ||
		mediaPath == "" || !filepath.IsAbs(mediaPath) || filepath.Clean(mediaPath) != mediaPath {
		return SegmentScreeningSummary{}, fmt.Errorf("segment screening summary request is invalid")
	}
	base := SegmentScreeningSummary{ClipHash: clipHash}
	tags, state := ReadSidecarTagsState(mediaPath)
	if state == SidecarAbsent || state == SidecarValid && tags.SegmentScreening == nil {
		base.State = ScreeningSummaryNotScreened
		base.ReasonCode = ScreeningSummaryReasonNotAttached
		return base, nil
	}
	if state != SidecarValid {
		base.State = ScreeningSummaryUnavailable
		base.ReasonCode = ScreeningSummaryReasonSidecarInvalid
		return base, nil
	}

	reference := *tags.SegmentScreening
	base.SubjectSHA256 = reference.SubjectSHA256
	base.EvidenceSHA256 = reference.EvidenceSHA256
	subject, err := s.evidence.GetSegmentScreeningSubject(ctx, reference.SubjectSHA256)
	if err != nil {
		base.State = ScreeningSummaryUnavailable
		base.ReasonCode = ScreeningSummaryReasonEvidenceUnavailable
		return base, fmt.Errorf("read segment screening summary subject: %w", err)
	}
	evidence, err := s.evidence.GetSegmentScreeningEvidence(ctx, reference.EvidenceSHA256)
	if err != nil {
		base.State = ScreeningSummaryUnavailable
		base.ReasonCode = ScreeningSummaryReasonEvidenceUnavailable
		return base, fmt.Errorf("read segment screening summary evidence: %w", err)
	}
	if VerifySegmentScreeningSubject(subject, clipHash, tags) != nil ||
		ValidateSegmentScreeningEvidence(evidence) != nil ||
		evidence.SubjectSHA256 != subject.SHA256 || evidence.SHA256 != reference.EvidenceSHA256 {
		base.State = ScreeningSummaryUnavailable
		base.ReasonCode = ScreeningSummaryReasonEvidenceDrift
		return base, fmt.Errorf("segment screening summary evidence drifted")
	}
	_, playbackMatches, err := s.inspect(
		ctx, mediaPath, subject.PlaybackSHA256, subject.PlaybackBytes, subject.CatalogHash,
	)
	if err != nil {
		base.State = ScreeningSummaryUnavailable
		base.ReasonCode = ScreeningSummaryReasonEvidenceUnavailable
		return base, fmt.Errorf("inspect segment screening summary playback: %w", err)
	}
	if !playbackMatches {
		base.State = ScreeningSummaryUnavailable
		base.ReasonCode = ScreeningSummaryReasonEvidenceDrift
		return base, fmt.Errorf("segment screening summary playback drifted")
	}

	byAxis := make(map[SegmentScreeningAxis]SegmentScreeningResult, len(evidence.Results))
	for _, result := range evidence.Results {
		byAxis[result.Axis] = result
	}
	base.Axes = make([]SegmentScreeningAxisSummary, 0, len(segmentScreeningAxisOrder))
	for _, axis := range segmentScreeningAxisOrder {
		result, ok := byAxis[axis]
		if !ok {
			base.State = ScreeningSummaryUnavailable
			base.ReasonCode = ScreeningSummaryReasonEvidenceDrift
			base.Axes = nil
			return base, fmt.Errorf("segment screening summary axis coverage drifted")
		}
		base.Axes = append(base.Axes, SegmentScreeningAxisSummary{
			Axis: axis, Outcome: result.Outcome, ReasonCode: result.ReasonCode,
			EvidenceSHA256: result.AuthoritySHA256,
		})
	}
	if subject.SourceID != "" && subject.AcquisitionID != "" {
		rightsResult := byAxis[ScreenRights]
		rightsRecord, err := s.evidence.GetSegmentScreeningAxisRecord(ctx, rightsResult.AuthoritySHA256)
		if err != nil {
			base.State = ScreeningSummaryUnavailable
			base.ReasonCode = ScreeningSummaryReasonEvidenceUnavailable
			base.Axes = nil
			return base, fmt.Errorf("read segment screening summary rights record: %w", err)
		}
		scope := FillerRightsScope{
			SourceID: subject.SourceID, AcquisitionID: subject.AcquisitionID,
			SourceMasterSHA256: subject.SourceMasterSHA256,
			PolicySHA256:       rightsRecord.Profile.PolicySHA256, Use: FillerBroadcastUse,
		}
		if rightsRecord.SHA256 != rightsResult.AuthoritySHA256 || rightsRecord.SubjectSHA256 != subject.SHA256 ||
			rightsRecord.Profile.Axis != ScreenRights || ValidateFillerRightsScope(scope) != nil {
			base.State = ScreeningSummaryUnavailable
			base.ReasonCode = ScreeningSummaryReasonEvidenceDrift
			base.Axes = nil
			return base, fmt.Errorf("segment screening summary rights context drifted")
		}
		base.RightsScope = &scope
	}
	decision := cloneAirworthinessDecision(evidence.Airworthiness)
	base.State = ScreeningSummaryAvailable
	base.Outcome = screeningSummaryOutcomeFromClosedResults(base.Axes, decision.Verdict)
	base.Airworthiness = &decision
	base.AssessedAt = evidence.AssessedAt
	if err := ValidateSegmentScreeningSummary(base); err != nil {
		return SegmentScreeningSummary{}, err
	}
	return base, nil
}

func ValidateSegmentScreeningSummary(summary SegmentScreeningSummary) error {
	if !isContentHash(summary.ClipHash) || !validScreeningReasonCodeOrEmpty(summary.ReasonCode) {
		return fmt.Errorf("segment screening summary identity is invalid")
	}
	switch summary.State {
	case ScreeningSummaryNotScreened:
		if summary.ReasonCode != ScreeningSummaryReasonNotAttached || summary.SubjectSHA256 != "" || summary.EvidenceSHA256 != "" ||
			!emptySegmentScreeningSummary(summary) {
			return fmt.Errorf("not-screened summary carries evidence")
		}
		return nil
	case ScreeningSummaryUnavailable:
		if summary.ReasonCode != ScreeningSummaryReasonSidecarInvalid &&
			summary.ReasonCode != ScreeningSummaryReasonEvidenceUnavailable &&
			summary.ReasonCode != ScreeningSummaryReasonEvidenceDrift {
			return fmt.Errorf("unavailable summary reason is invalid")
		}
		identitiesAbsent := summary.SubjectSHA256 == "" && summary.EvidenceSHA256 == ""
		identitiesPresent := isContentHash(summary.SubjectSHA256) && isContentHash(summary.EvidenceSHA256)
		if (!identitiesAbsent && !identitiesPresent) ||
			summary.ReasonCode == ScreeningSummaryReasonSidecarInvalid && !identitiesAbsent ||
			summary.ReasonCode != ScreeningSummaryReasonSidecarInvalid && !identitiesPresent ||
			!emptySegmentScreeningSummary(summary) {
			return fmt.Errorf("unavailable summary carries partial or semantic evidence")
		}
		return nil
	case ScreeningSummaryAvailable:
		if summary.ReasonCode != "" || !isContentHash(summary.SubjectSHA256) || !isContentHash(summary.EvidenceSHA256) ||
			(summary.Outcome != ScreenPass && summary.Outcome != ScreenReject && summary.Outcome != ScreenHold) ||
			len(summary.Axes) != len(segmentScreeningAxisOrder) || summary.Airworthiness == nil || summary.AssessedAt.IsZero() ||
			summary.AssessedAt != summary.AssessedAt.UTC() ||
			fillerairworthiness.ValidateDecision(*summary.Airworthiness) != nil ||
			summary.Airworthiness.SubjectSHA256 != summary.SubjectSHA256 {
			return fmt.Errorf("available summary is incomplete")
		}
		if summary.RightsScope != nil && ValidateFillerRightsScope(*summary.RightsScope) != nil {
			return fmt.Errorf("available summary rights scope is invalid")
		}
		for index, axis := range summary.Axes {
			if axis.Axis != segmentScreeningAxisOrder[index] ||
				validateSegmentScreeningResult(SegmentScreeningResult{Axis: axis.Axis, Outcome: axis.Outcome, AuthoritySHA256: axis.EvidenceSHA256, ReasonCode: axis.ReasonCode}) != nil {
				return fmt.Errorf("available summary axis is invalid or unordered")
			}
		}
		if summary.Outcome != screeningSummaryOutcomeFromClosedResults(summary.Axes, summary.Airworthiness.Verdict) {
			return fmt.Errorf("available summary outcome disagrees with its closed results")
		}
		return nil
	default:
		return fmt.Errorf("segment screening summary state is invalid")
	}
}

func screeningSummaryOutcomeFromClosedResults(axes []SegmentScreeningAxisSummary, verdict fillerairworthiness.Verdict) SegmentScreeningOutcome {
	if verdict == fillerairworthiness.VerdictReject ||
		slices.ContainsFunc(axes, func(axis SegmentScreeningAxisSummary) bool { return axis.Outcome == ScreenReject }) {
		return ScreenReject
	}
	if verdict == fillerairworthiness.VerdictPass &&
		!slices.ContainsFunc(axes, func(axis SegmentScreeningAxisSummary) bool { return axis.Outcome != ScreenPass }) {
		return ScreenPass
	}
	return ScreenHold
}

func emptySegmentScreeningSummary(summary SegmentScreeningSummary) bool {
	return summary.Outcome == "" && len(summary.Axes) == 0 && summary.RightsScope == nil && summary.Airworthiness == nil && summary.AssessedAt.IsZero()
}

func validScreeningReasonCodeOrEmpty(value string) bool {
	return value == "" || validScreeningReasonCode(value)
}

var _ SegmentScreeningSummaryEvidenceReader = (*FileSegmentScreeningEvidenceRepository)(nil)
