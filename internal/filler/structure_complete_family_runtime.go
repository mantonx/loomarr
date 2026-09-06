package filler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

// StructureCompleteFamilyEvidenceRepository exposes the completed-operation lookup needed to run
// one paid complete-video family safely across process restarts.
type StructureCompleteFamilyEvidenceRepository interface {
	PutStructureAssessmentEvidence(context.Context, fillerstructure.RecordedAssessment) error
	GetStructureAssessmentEvidence(context.Context, string) (fillerstructure.RecordedAssessment, error)
	FindStructureAssessmentEvidence(context.Context, fillerstructure.Source, fillerstructure.AssessmentMedia, fillerstructure.AssessorProfile) (fillerstructure.RecordedAssessment, bool, error)
}

// StructureCompleteFamilyRuntime owns one assessor family's durable call/replay seam. Reduction
// remains outside it so this runtime never sees peer answers.
type StructureCompleteFamilyRuntime struct {
	assessor CompleteTimelineStructureAssessor
	evidence StructureCompleteFamilyEvidenceRepository
	profile  fillerstructure.AssessorProfile
}

func NewStructureCompleteFamilyRuntime(assessor CompleteTimelineStructureAssessor, evidence StructureCompleteFamilyEvidenceRepository) (*StructureCompleteFamilyRuntime, error) {
	if assessor == nil || evidence == nil {
		return nil, errors.New("structure complete family runtime requires assessor and evidence repository")
	}
	profile := assessor.Profile()
	if err := fillerstructure.ValidateAssessorProfile(profile); err != nil {
		return nil, fmt.Errorf("structure complete family profile: %w", err)
	}
	return &StructureCompleteFamilyRuntime{assessor: assessor, evidence: evidence, profile: profile}, nil
}

func (r *StructureCompleteFamilyRuntime) Profile() fillerstructure.AssessorProfile {
	if r == nil {
		return fillerstructure.AssessorProfile{}
	}
	return r.profile
}

// AssessWithEvidence returns a fully reloaded call record. A completed operation is reused; an
// absent operation is called once and published before it can be returned.
func (r *StructureCompleteFamilyRuntime) AssessWithEvidence(ctx context.Context, media StructureAssessmentMedia) (fillerstructure.RecordedAssessment, error) {
	if r == nil || r.assessor == nil || r.evidence == nil {
		return fillerstructure.RecordedAssessment{}, errors.New("structure complete family runtime is unavailable")
	}
	if err := media.Source.validate(); err != nil || !filepath.IsAbs(media.FullPath) || filepath.Clean(media.FullPath) != media.FullPath {
		return fillerstructure.RecordedAssessment{}, errors.New("structure complete family media path or source is invalid")
	}
	source := fillerstructure.Source{SHA256: media.Source.SHA256, Bytes: media.Source.Bytes, DurationMS: media.Source.DurationMs}
	if err := fillerstructure.ValidateAssessmentMedia(source, media.Assessment); err != nil {
		return fillerstructure.RecordedAssessment{}, err
	}
	recorded, found, err := r.evidence.FindStructureAssessmentEvidence(ctx, source, media.Assessment, r.profile)
	if err != nil {
		return fillerstructure.RecordedAssessment{}, fmt.Errorf("find complete family evidence: %w", err)
	}
	if !found {
		recorded, err = r.assessor.AssessCompleteTimeline(ctx, media)
		if err != nil {
			return fillerstructure.RecordedAssessment{}, fmt.Errorf("assess complete family: %w", err)
		}
		if err := validateCompleteFamilyRecorded(source, media.Assessment, r.profile, recorded); err != nil {
			return fillerstructure.RecordedAssessment{}, err
		}
		if err := r.evidence.PutStructureAssessmentEvidence(ctx, recorded); err != nil {
			return fillerstructure.RecordedAssessment{}, fmt.Errorf("persist complete family evidence: %w", err)
		}
	}
	replayed, err := r.evidence.GetStructureAssessmentEvidence(ctx, recorded.Record.SHA256)
	if err != nil {
		return fillerstructure.RecordedAssessment{}, fmt.Errorf("replay complete family evidence: %w", err)
	}
	if err := validateCompleteFamilyRecorded(source, media.Assessment, r.profile, replayed); err != nil || !reflect.DeepEqual(replayed, recorded) {
		return fillerstructure.RecordedAssessment{}, errors.New("replayed complete family evidence drifted")
	}
	return replayed, nil
}

func validateCompleteFamilyRecorded(source fillerstructure.Source, media fillerstructure.AssessmentMedia, profile fillerstructure.AssessorProfile, recorded fillerstructure.RecordedAssessment) error {
	if err := fillerstructure.ValidateRecordedAssessment(recorded); err != nil {
		return err
	}
	if recorded.Record.Source != source || recorded.Record.Media != media || recorded.Record.Assessor != profile {
		return errors.New("complete family recorded assessment drifted authority")
	}
	return nil
}

var _ StructureCompleteFamilyEvidenceRepository = (*FileStructureAssessmentEvidenceRepository)(nil)
