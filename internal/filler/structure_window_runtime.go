package filler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

// CompleteWindowStructureAssessor sees one exact window and no peer answers. Ordinary provider
// failures return a recorded operational-failure assessment; error means no trustworthy evidence.
type CompleteWindowStructureAssessor interface {
	Profile() fillerstructure.AssessorProfile
	AssessWindow(context.Context, fillerstructurewindow.MediaSet, StructureAssessmentWindowMedia) (fillerstructurewindow.RecordedAssessment, error)
}

// StructureWindowEvidenceRepository commits and reloads each complete call before the next call,
// each family stitch before the next family, and the final decision before return.
type StructureWindowEvidenceRepository interface {
	PutStructureWindowAssessmentEvidence(context.Context, fillerstructurewindow.RecordedAssessment) error
	GetStructureWindowAssessmentEvidence(context.Context, fillerstructurewindow.MediaSet, string) (fillerstructurewindow.RecordedAssessment, error)
	FindStructureWindowAssessmentEvidence(context.Context, fillerstructurewindow.MediaSet, int, fillerstructure.AssessorProfile) (fillerstructurewindow.RecordedAssessment, bool, error)
	PutStructureWindowStitch(context.Context, fillerstructurewindow.StitchResult) error
	PutStructureDecisionArtifact(context.Context, fillerstructure.Artifact) error
}

// StructureWindowAssessmentRuntime owns complete preparation, family-major serial execution,
// deterministic stitching, and the same provider-neutral reduction used by short sources.
type StructureWindowAssessmentRuntime struct {
	families          []*StructureWindowFamilyRuntime
	profiles          []fillerstructure.AssessorProfile
	preparer          StructureAssessmentWindowMediaPreparer
	evidence          StructureWindowEvidenceRepository
	boundaryTolerance int64
	now               func() time.Time
}

func NewStructureWindowAssessmentRuntime(assessors []CompleteWindowStructureAssessor, preparer StructureAssessmentWindowMediaPreparer, evidence StructureWindowEvidenceRepository, boundaryTolerance int64, now func() time.Time) (*StructureWindowAssessmentRuntime, error) {
	if len(assessors) < 2 || preparer == nil || evidence == nil || boundaryTolerance < 0 ||
		boundaryTolerance >= fillerstructurewindow.CanonicalProfile().ContextOverlapMS || now == nil {
		return nil, errors.New("structure window runtime requires two assessors, media preparer, evidence repository, tolerance, and clock")
	}
	profiles := make([]fillerstructure.AssessorProfile, 0, len(assessors))
	for _, assessor := range assessors {
		if assessor == nil {
			return nil, errors.New("structure window runtime contains a nil assessor")
		}
		profiles = append(profiles, assessor.Profile())
	}
	if err := fillerstructure.ValidateAssessorProfiles(profiles); err != nil {
		return nil, fmt.Errorf("structure window runtime profiles: %w", err)
	}
	families := make([]*StructureWindowFamilyRuntime, 0, len(assessors))
	for _, assessor := range assessors {
		family, err := NewStructureWindowFamilyRuntime(assessor, evidence, boundaryTolerance)
		if err != nil {
			return nil, err
		}
		families = append(families, family)
	}
	return &StructureWindowAssessmentRuntime{
		families: families, profiles: slices.Clone(profiles),
		preparer: preparer, evidence: evidence, boundaryTolerance: boundaryTolerance, now: now,
	}, nil
}

func (r *StructureWindowAssessmentRuntime) Assess(ctx context.Context, input StructureAssessmentSource) (fillerstructure.Artifact, error) {
	if r == nil || len(r.families) < 2 || len(r.profiles) != len(r.families) || r.preparer == nil || r.evidence == nil || r.now == nil {
		return fillerstructure.Artifact{}, errors.New("structure window runtime is unavailable")
	}
	if err := input.Source.validate(); err != nil || !filepath.IsAbs(input.FullPath) || filepath.Clean(input.FullPath) != input.FullPath {
		return fillerstructure.Artifact{}, errors.New("structure window runtime source is invalid")
	}
	source := fillerstructure.Source{SHA256: input.Source.SHA256, Bytes: input.Source.Bytes, DurationMS: input.Source.DurationMs}
	plan, err := fillerstructurewindow.NewPlan(source)
	if err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("plan structure windows: %w", err)
	}
	prepared, err := r.preparer.PrepareWindows(ctx, input, plan)
	if err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("prepare structure windows: %w", err)
	}
	return r.AssessPrepared(ctx, input, prepared)
}

// AssessPrepared executes the independent families over one already verified media set. Hosted
// adapters use this seam after local preparation succeeds, so capability refresh and paid calls
// cannot happen before media preflight. The full source and plan are rederived here rather than
// trusting the adapter that supplied prepared.
func (r *StructureWindowAssessmentRuntime) AssessPrepared(ctx context.Context, input StructureAssessmentSource, prepared StructureAssessmentWindowMediaSet) (fillerstructure.Artifact, error) {
	if r == nil || len(r.families) < 2 || len(r.profiles) != len(r.families) || r.evidence == nil || r.now == nil {
		return fillerstructure.Artifact{}, errors.New("structure window runtime is unavailable")
	}
	if err := ValidatePreparedStructureAssessmentWindows(input, prepared); err != nil {
		return fillerstructure.Artifact{}, err
	}
	source := fillerstructure.Source{SHA256: input.Source.SHA256, Bytes: input.Source.Bytes, DurationMS: input.Source.DurationMs}
	requestInput, err := fillerstructurewindow.ReducerInput(prepared.Authority)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	request := fillerstructure.Request{Source: source, Input: requestInput, BoundaryToleranceMS: r.boundaryTolerance}
	for family, familyRuntime := range r.families {
		stitched, err := familyRuntime.Assess(ctx, prepared)
		if err != nil {
			return fillerstructure.Artifact{}, fmt.Errorf("assess structure window family %d: %w", family, err)
		}
		candidateInput, candidate, err := fillerstructurewindow.ReducerCandidate(stitched)
		if err != nil {
			return fillerstructure.Artifact{}, fmt.Errorf("project structure window assessor %d candidate: %w", family, err)
		}
		if !reflect.DeepEqual(candidateInput, requestInput) {
			return fillerstructure.Artifact{}, fmt.Errorf("structure window assessor %d candidate drifted common input", family)
		}
		request.Candidates = append(request.Candidates, candidate)
	}
	artifact, err := fillerstructure.NewArtifact(request, r.now())
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	if err := r.evidence.PutStructureDecisionArtifact(ctx, artifact); err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("persist structure window decision: %w", err)
	}
	return artifact, nil
}

// ValidatePreparedStructureAssessmentWindows closes the local preflight boundary shared by
// provider-neutral execution and hosted adapters. It performs no inference, persistence, or I/O.
func ValidatePreparedStructureAssessmentWindows(input StructureAssessmentSource, prepared StructureAssessmentWindowMediaSet) error {
	if err := input.Source.validate(); err != nil || !filepath.IsAbs(input.FullPath) || filepath.Clean(input.FullPath) != input.FullPath {
		return errors.New("structure window runtime source is invalid")
	}
	source := fillerstructure.Source{SHA256: input.Source.SHA256, Bytes: input.Source.Bytes, DurationMS: input.Source.DurationMs}
	plan, err := fillerstructurewindow.NewPlan(source)
	if err != nil {
		return fmt.Errorf("plan structure windows: %w", err)
	}
	if prepared.Source != input.Source || prepared.Authority.Plan.SHA256 != plan.SHA256 ||
		!reflect.DeepEqual(prepared.Authority.Plan, plan) || validatePreparedStructureAssessmentWindows(prepared) != nil ||
		structureWindowSetReusesSourcePath(prepared, input.FullPath) {
		return errors.New("structure window preparer drifted source, plan, or media authority")
	}
	return nil
}

func validRecordedWindowAuthority(set fillerstructurewindow.MediaSet, window StructureAssessmentWindowMedia, profile fillerstructure.AssessorProfile, ordinal int, recorded fillerstructurewindow.RecordedAssessment) bool {
	return fillerstructurewindow.ValidateRecordedAssessment(recorded) == nil &&
		recorded.Assessment.WindowOrdinal == ordinal && recorded.Assessment.Media == window.Media.Media &&
		reflect.DeepEqual(recorded.Assessment.Assessor, profile) && reflect.DeepEqual(recorded.Record.MediaSet, set)
}

func structureWindowSetReusesSourcePath(prepared StructureAssessmentWindowMediaSet, sourcePath string) bool {
	for _, window := range prepared.Windows {
		if window.FullPath == sourcePath {
			return true
		}
	}
	return false
}

var _ CompleteTimelineStructureDecisioner = (*StructureWindowAssessmentRuntime)(nil)
