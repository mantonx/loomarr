package filler

import (
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

type structureDecisionProjectionInterval struct {
	role          StructureSegmentRole
	disposition   StructureSegmentDisposition
	observationID string
}

// structureDecisionProjectionAuthority is an in-memory capability derived only by replaying an
// attached artifact. It is never serialized and a complete-timeline observation cannot mint one.
type structureDecisionProjectionAuthority struct {
	artifactSHA256 string
	reducerVersion string
	intervals      map[[2]int64]structureDecisionProjectionInterval
}

func newStructureDecisionProjectionAuthority(source SplitSourceAsset, artifact fillerstructure.Artifact) (*structureDecisionProjectionAuthority, error) {
	if err := source.validate(); err != nil {
		return nil, err
	}
	if err := fillerstructure.ValidateArtifact(artifact); err != nil {
		return nil, err
	}
	decision := artifact.Decision
	if decision.Status != fillerstructure.StatusConfirmed {
		return nil, errors.New("source structure decision is held")
	}
	if decision.Source.SHA256 != source.SHA256 || decision.Source.Bytes != source.Bytes || decision.Source.DurationMS != source.DurationMs {
		return nil, errors.New("source structure decision binds another source")
	}
	if _, err := projectedStructureKind(decision.Unit); err != nil {
		return nil, err
	}
	authority := &structureDecisionProjectionAuthority{
		artifactSHA256: artifact.SHA256,
		reducerVersion: artifact.ReducerVersion,
		intervals:      make(map[[2]int64]structureDecisionProjectionInterval, len(decision.Segments)),
	}
	end := int64(0)
	for index, segment := range decision.Segments {
		role := StructureSegmentRole(segment.Role)
		disposition, err := projectedStructureDisposition(segment.Disposition)
		if err != nil || !validStructureSegmentRole(role) || segment.StartMS != end || segment.EndMS <= segment.StartMS || segment.EndMS > source.DurationMs {
			return nil, fmt.Errorf("source structure decision interval %d is invalid", index)
		}
		if disposition == StructureKeep && !certifiedFillerRole(role) || disposition == StructureDiscard && role != SegmentRoleProgrammeFragment && role != SegmentRoleNonFiller {
			return nil, fmt.Errorf("source structure decision interval %d role and disposition disagree", index)
		}
		span := [2]int64{segment.StartMS, segment.EndMS}
		if _, duplicate := authority.intervals[span]; duplicate {
			return nil, fmt.Errorf("source structure decision repeats interval %d", index)
		}
		authority.intervals[span] = structureDecisionProjectionInterval{
			role: role, disposition: disposition,
			observationID: fmt.Sprintf("%sinterval-%04d", structureDecisionObservationPrefix, index+1),
		}
		end = segment.EndMS
	}
	if len(decision.Segments) == 0 || end != source.DurationMs {
		return nil, errors.New("source structure decision intervals do not cover the source")
	}
	return authority, nil
}

// ValidateStructureDecisionProjection proves that the proposal assessment is an exact projection
// of one replayable, confirmed independent-assessor decision. It grants no certification itself.
func ValidateStructureDecisionProjection(assessment SourceStructureAssessment, artifact fillerstructure.Artifact) error {
	authority, err := newStructureDecisionProjectionAuthority(assessment.Source, artifact)
	if err != nil {
		return err
	}
	if err := validateSourceStructureAssessment(assessment, authority); err != nil {
		return err
	}
	decision := artifact.Decision
	kind, err := projectedStructureKind(decision.Unit)
	if err != nil || assessment.Kind != kind || len(decision.Segments) != len(assessment.Plan) {
		return errors.New("source structure decision unit or interval count does not match")
	}
	for index, decided := range decision.Segments {
		planned := assessment.Plan[index]
		disposition, err := projectedStructureDisposition(decided.Disposition)
		if err != nil || planned.StartMs != decided.StartMS || planned.EndMs != decided.EndMS ||
			planned.Role != StructureSegmentRole(decided.Role) || planned.Disposition != disposition {
			return fmt.Errorf("source structure decision interval %d does not match", index)
		}
	}
	return nil
}

func validateSourceStructureAssessmentOrProjection(assessment SourceStructureAssessment, artifact *fillerstructure.Artifact) error {
	strictErr := ValidateSourceStructureAssessment(assessment)
	if strictErr == nil {
		return nil
	}
	if artifact != nil && ValidateStructureDecisionProjection(assessment, *artifact) == nil {
		return nil
	}
	return strictErr
}

func projectedStructureKind(unit fillerstructure.Unit) (SourceStructureKind, error) {
	switch unit {
	case fillerstructure.UnitStandalone, fillerstructure.UnitProgrammeExcerpt:
		return StructureSingleUnit, nil
	case fillerstructure.UnitCompilation:
		return StructureCompilationBreak, nil
	case fillerstructure.UnitProgrammeSpots:
		return StructureProgrammeSpots, nil
	default:
		return "", fmt.Errorf("source structure decision unit %q cannot be projected", unit)
	}
}

func projectedStructureDisposition(disposition fillerstructure.Disposition) (StructureSegmentDisposition, error) {
	switch disposition {
	case fillerstructure.DispositionFillerCandidate:
		return StructureKeep, nil
	case fillerstructure.DispositionNonFiller:
		return StructureDiscard, nil
	default:
		return "", fmt.Errorf("source structure decision disposition %q cannot be projected", disposition)
	}
}
