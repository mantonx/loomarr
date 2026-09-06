package filler

import (
	"fmt"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const structureDecisionObservationPrefix = "complete-decision-"

// ProjectConfirmedStructureDecision constructs the V67 complete-plan assessment from one replayed
// provider-neutral decision. Detector observations remain inspectable context but cannot create,
// move, or resolve a boundary in the projected plan.
func ProjectConfirmedStructureDecision(source SplitSourceAsset, detector *SourceStructureAssessment, artifact fillerstructure.Artifact) (SourceStructureAssessment, error) {
	authority, err := newStructureDecisionProjectionAuthority(source, artifact)
	if err != nil {
		return SourceStructureAssessment{}, fmt.Errorf("project structure decision: %w", err)
	}
	decision := artifact.Decision

	observations, assessedAt, err := projectedDetectorContext(source, detector, artifact)
	if err != nil {
		return SourceStructureAssessment{}, err
	}
	claims := make([]StructureRoleClaim, 0, len(decision.Segments))
	for index, segment := range decision.Segments {
		role := StructureSegmentRole(segment.Role)
		disposition, dispositionErr := projectedStructureDisposition(segment.Disposition)
		if dispositionErr != nil || !validStructureSegmentRole(role) || disposition == StructureUnresolved {
			return SourceStructureAssessment{}, fmt.Errorf("project structure decision: interval %d cannot enter V67", index)
		}
		intervalID := fmt.Sprintf("%sinterval-%04d", structureDecisionObservationPrefix, index+1)
		observations = append(observations, StructureObservation{
			ID: intervalID, Kind: ObservationCompleteTimelineDecision, Effect: ObservationContextOnly,
			StartMs: segment.StartMS, EndMs: segment.EndMS, Producer: artifact.ReducerVersion,
			EvidenceSHA256: artifact.SHA256,
		})
		claims = append(claims, StructureRoleClaim{
			StartMs: segment.StartMS, EndMs: segment.EndMS, Role: role,
			EvidenceIDs: []string{intervalID}, Reason: fillerstructure.ReasonAgreement,
		})
		if segment.EndMS < source.DurationMs {
			observations = append(observations, StructureObservation{
				ID:   fmt.Sprintf("%sboundary-%04d", structureDecisionObservationPrefix, index+1),
				Kind: ObservationCompleteTimelineDecision, Effect: ObservationProposesBoundary,
				StartMs: segment.EndMS, EndMs: segment.EndMS, Producer: artifact.ReducerVersion,
				EvidenceSHA256: artifact.SHA256,
			})
		}
	}
	assessment, err := assessSourceStructure(SourceStructureInput{
		Source: source, Observations: observations, RoleClaims: claims, AssessedAt: assessedAt,
	}, authority)
	if err != nil {
		return SourceStructureAssessment{}, fmt.Errorf("project structure decision: %w", err)
	}
	if err := ValidateStructureDecisionProjection(assessment, artifact); err != nil {
		return SourceStructureAssessment{}, fmt.Errorf("project structure decision: %w", err)
	}
	return assessment, nil
}

func projectedDetectorContext(source SplitSourceAsset, detector *SourceStructureAssessment, artifact fillerstructure.Artifact) ([]StructureObservation, time.Time, error) {
	assessedAt := artifact.DecidedAt
	if detector == nil {
		return nil, assessedAt, nil
	}
	if err := validateSourceStructureAssessmentOrProjection(*detector, &artifact); err != nil || detector.Source != source {
		return nil, time.Time{}, fmt.Errorf("project structure decision: detector assessment is invalid or source-drifted")
	}
	if detector.AssessedAt.After(assessedAt) {
		assessedAt = detector.AssessedAt
	}
	observations := make([]StructureObservation, 0, len(detector.Observations))
	for _, observation := range detector.Observations {
		if strings.HasPrefix(observation.ID, structureDecisionObservationPrefix) {
			continue
		}
		observation.Effect = ObservationContextOnly
		observations = append(observations, observation)
	}
	return observations, assessedAt, nil
}
