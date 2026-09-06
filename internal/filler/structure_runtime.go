package filler

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

type StructureAssessmentSource struct {
	Source   SplitSourceAsset
	FullPath string
}

type StructureAssessmentMedia struct {
	Source     SplitSourceAsset
	Assessment fillerstructure.AssessmentMedia
	FullPath   string
}

// StructureAssessmentMediaPreparer produces one durable canonical derivative
// before either independent assessor runs.
type StructureAssessmentMediaPreparer interface {
	Prepare(context.Context, StructureAssessmentSource) (StructureAssessmentMedia, error)
}

// CompleteTimelineStructureAssessor receives no peer answers. Expected provider failures must be
// returned as source-bound Candidate.Failure evidence; error is reserved for missing authority.
type CompleteTimelineStructureAssessor interface {
	Profile() fillerstructure.AssessorProfile
	AssessCompleteTimeline(context.Context, StructureAssessmentMedia) (fillerstructure.RecordedAssessment, error)
}

// CompleteTimelineStructureDecisioner is the split stage's deep assessment interface. The
// implementation owns independent execution, evidence persistence, and deterministic reduction.
type CompleteTimelineStructureDecisioner interface {
	Assess(context.Context, StructureAssessmentSource) (fillerstructure.Artifact, error)
}

// StructureAssessmentEvidenceRepository must durably commit each record and exact response before
// reduction, then commit the reduced artifact before the runtime returns it.
type StructureAssessmentEvidenceRepository interface {
	PutStructureAssessmentEvidence(context.Context, fillerstructure.RecordedAssessment) error
	PutStructureDecisionArtifact(context.Context, fillerstructure.Artifact) error
}

// StructureAssessmentRuntime owns serial independent execution, evidence persistence, and shared
// reduction. Provider configuration and accounting reservations remain inside each assessor adapter.
type StructureAssessmentRuntime struct {
	assessors         []CompleteTimelineStructureAssessor
	preparer          StructureAssessmentMediaPreparer
	evidence          StructureAssessmentEvidenceRepository
	boundaryTolerance int64
	now               func() time.Time
}

func NewStructureAssessmentRuntime(assessors []CompleteTimelineStructureAssessor, preparer StructureAssessmentMediaPreparer, evidence StructureAssessmentEvidenceRepository, boundaryTolerance int64, now func() time.Time) (*StructureAssessmentRuntime, error) {
	if len(assessors) < 2 || preparer == nil || evidence == nil || boundaryTolerance < 0 || now == nil {
		return nil, fmt.Errorf("structure assessment runtime requires two assessors, media preparer, evidence repository, tolerance, and clock")
	}
	profiles := make([]fillerstructure.AssessorProfile, 0, len(assessors))
	for _, assessor := range assessors {
		if assessor == nil {
			return nil, fmt.Errorf("structure assessment runtime contains a nil assessor")
		}
		profiles = append(profiles, assessor.Profile())
	}
	if err := fillerstructure.ValidateAssessorProfiles(profiles); err != nil {
		return nil, fmt.Errorf("structure assessment runtime profiles: %w", err)
	}
	return &StructureAssessmentRuntime{
		assessors:         append([]CompleteTimelineStructureAssessor(nil), assessors...),
		preparer:          preparer,
		evidence:          evidence,
		boundaryTolerance: boundaryTolerance, now: now,
	}, nil
}

func (r *StructureAssessmentRuntime) Assess(ctx context.Context, input StructureAssessmentSource) (fillerstructure.Artifact, error) {
	if r == nil || len(r.assessors) < 2 || r.preparer == nil || r.now == nil {
		return fillerstructure.Artifact{}, fmt.Errorf("structure assessment runtime is unavailable")
	}
	if err := input.Source.validate(); err != nil || !filepath.IsAbs(input.FullPath) || filepath.Clean(input.FullPath) != input.FullPath {
		return fillerstructure.Artifact{}, fmt.Errorf("structure assessment runtime source is invalid")
	}
	media, err := r.preparer.Prepare(ctx, input)
	if err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("prepare structure assessment media: %w", err)
	}
	if media.Source != input.Source || !filepath.IsAbs(media.FullPath) || filepath.Clean(media.FullPath) != media.FullPath || media.FullPath == input.FullPath {
		return fillerstructure.Artifact{}, fmt.Errorf("structure assessment preparer drifted source or path")
	}
	source := fillerstructure.Source{SHA256: media.Source.SHA256, Bytes: media.Source.Bytes, DurationMS: media.Source.DurationMs}
	if err := fillerstructure.ValidateAssessmentMedia(source, media.Assessment); err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("structure assessment preparer returned invalid media authority")
	}
	assessmentInput, err := fillerstructure.NewCompleteVideoInput(source, media.Assessment)
	if err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("structure assessment preparer returned invalid input authority")
	}
	request := fillerstructure.Request{Source: source, Input: assessmentInput, BoundaryToleranceMS: r.boundaryTolerance}
	for index, assessor := range r.assessors {
		if err := ctx.Err(); err != nil {
			return fillerstructure.Artifact{}, err
		}
		recorded, err := assessor.AssessCompleteTimeline(ctx, media)
		if err != nil {
			return fillerstructure.Artifact{}, fmt.Errorf("complete-timeline assessor %d produced no authority: %w", index, err)
		}
		if err := fillerstructure.ValidateRecordedAssessment(recorded); err != nil {
			return fillerstructure.Artifact{}, fmt.Errorf("complete-timeline assessor %d returned invalid evidence: %w", index, err)
		}
		candidate, err := recorded.Record.Candidate()
		if err != nil {
			return fillerstructure.Artifact{}, fmt.Errorf("complete-timeline assessor %d returned invalid candidate: %w", index, err)
		}
		if candidate.Source != source || candidate.InputSHA256 != assessmentInput.SHA256 || !reflect.DeepEqual(fillerstructure.Profile(candidate.Assessor), assessor.Profile()) {
			return fillerstructure.Artifact{}, fmt.Errorf("complete-timeline assessor %d drifted source, media, or profile", index)
		}
		if err := r.evidence.PutStructureAssessmentEvidence(ctx, recorded); err != nil {
			return fillerstructure.Artifact{}, fmt.Errorf("persist complete-timeline assessor %d evidence: %w", index, err)
		}
		request.Candidates = append(request.Candidates, candidate)
	}
	artifact, err := fillerstructure.NewArtifact(request, r.now())
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	if err := r.evidence.PutStructureDecisionArtifact(ctx, artifact); err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("persist complete-timeline decision: %w", err)
	}
	return artifact, nil
}

var _ CompleteTimelineStructureDecisioner = (*StructureAssessmentRuntime)(nil)
