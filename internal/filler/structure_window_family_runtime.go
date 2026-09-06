package filler

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

// StructureWindowFamilyRuntime owns one assessor family's complete serial window run. It commits
// and replays every call before advancing, then persists the deterministic whole-source stitch.
// Running families through separate instances prevents either family from observing peer answers.
type StructureWindowFamilyRuntime struct {
	assessor          CompleteWindowStructureAssessor
	profile           fillerstructure.AssessorProfile
	evidence          StructureWindowEvidenceRepository
	boundaryTolerance int64
}

func NewStructureWindowFamilyRuntime(assessor CompleteWindowStructureAssessor, evidence StructureWindowEvidenceRepository, boundaryTolerance int64) (*StructureWindowFamilyRuntime, error) {
	if assessor == nil || evidence == nil || boundaryTolerance < 0 ||
		boundaryTolerance >= fillerstructurewindow.CanonicalProfile().ContextOverlapMS {
		return nil, errors.New("structure window family runtime requires an assessor, evidence repository, and valid tolerance")
	}
	profile := assessor.Profile()
	if err := fillerstructure.ValidateAssessorProfile(profile); err != nil {
		return nil, fmt.Errorf("structure window family runtime profile: %w", err)
	}
	return &StructureWindowFamilyRuntime{
		assessor: assessor, profile: profile, evidence: evidence, boundaryTolerance: boundaryTolerance,
	}, nil
}

func (r *StructureWindowFamilyRuntime) Profile() fillerstructure.AssessorProfile {
	if r == nil {
		return fillerstructure.AssessorProfile{}
	}
	return r.profile
}

// Assess runs or resumes every window and returns one replayable source-level family stitch.
func (r *StructureWindowFamilyRuntime) Assess(ctx context.Context, prepared StructureAssessmentWindowMediaSet) (fillerstructurewindow.StitchResult, error) {
	evidence, err := r.AssessWithEvidence(ctx, prepared)
	if err != nil {
		return fillerstructurewindow.StitchResult{}, err
	}
	return evidence.Stitch, nil
}

// AssessWithEvidence returns the complete persisted call/accounting chain behind the stitch. It is
// used by certification; production reduction may use Assess when it needs only the stitch.
func (r *StructureWindowFamilyRuntime) AssessWithEvidence(ctx context.Context, prepared StructureAssessmentWindowMediaSet) (StructureWindowFamilyEvidence, error) {
	if r == nil || r.assessor == nil || r.evidence == nil || fillerstructure.ValidateAssessorProfile(r.profile) != nil {
		return StructureWindowFamilyEvidence{}, errors.New("structure window family runtime is unavailable")
	}
	wantSource := fillerstructure.Source{
		SHA256: prepared.Source.SHA256, Bytes: prepared.Source.Bytes, DurationMS: prepared.Source.DurationMs,
	}
	if prepared.Authority.Plan.Source != wantSource || validatePreparedStructureAssessmentWindows(prepared) != nil {
		return StructureWindowFamilyEvidence{}, errors.New("structure window family runtime media authority is invalid")
	}
	assessments := make([]fillerstructurewindow.Assessment, 0, len(prepared.Windows))
	recordedAssessments := make([]fillerstructurewindow.RecordedAssessment, 0, len(prepared.Windows))
	for ordinal, window := range prepared.Windows {
		if err := ctx.Err(); err != nil {
			return StructureWindowFamilyEvidence{}, err
		}
		recorded, found, err := r.evidence.FindStructureWindowAssessmentEvidence(ctx, prepared.Authority, ordinal, r.profile)
		if err != nil {
			return StructureWindowFamilyEvidence{}, fmt.Errorf("find structure window %d evidence: %w", ordinal, err)
		}
		if !found {
			recorded, err = r.assessor.AssessWindow(ctx, prepared.Authority, window)
			if err != nil {
				return StructureWindowFamilyEvidence{}, fmt.Errorf("structure window %d produced no authority: %w", ordinal, err)
			}
		}
		if !validRecordedWindowAuthority(prepared.Authority, window, r.profile, ordinal, recorded) {
			return StructureWindowFamilyEvidence{}, fmt.Errorf("structure window %d drifted authority", ordinal)
		}
		if !found {
			if err := r.evidence.PutStructureWindowAssessmentEvidence(ctx, recorded); err != nil {
				return StructureWindowFamilyEvidence{}, fmt.Errorf("persist structure window %d: %w", ordinal, err)
			}
		}
		replayed, err := r.evidence.GetStructureWindowAssessmentEvidence(ctx, prepared.Authority, recorded.Record.SHA256)
		if err != nil {
			return StructureWindowFamilyEvidence{}, fmt.Errorf("replay structure window %d evidence: %w", ordinal, err)
		}
		if !reflect.DeepEqual(replayed, recorded) {
			return StructureWindowFamilyEvidence{}, fmt.Errorf("replay structure window %d evidence drifted", ordinal)
		}
		assessments = append(assessments, replayed.Assessment)
		recordedAssessments = append(recordedAssessments, replayed)
	}
	stitched, err := fillerstructurewindow.Stitch(prepared.Authority, assessments, r.boundaryTolerance)
	if err != nil {
		return StructureWindowFamilyEvidence{}, fmt.Errorf("stitch structure windows: %w", err)
	}
	if err := r.evidence.PutStructureWindowStitch(ctx, stitched); err != nil {
		return StructureWindowFamilyEvidence{}, fmt.Errorf("persist structure window stitch: %w", err)
	}
	evidence, err := NewStructureWindowFamilyEvidence(recordedAssessments, stitched)
	if err != nil {
		return StructureWindowFamilyEvidence{}, fmt.Errorf("close structure window family evidence: %w", err)
	}
	return evidence, nil
}
