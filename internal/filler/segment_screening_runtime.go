package filler

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
)

// SegmentScreeningMedia supplies an evaluator with the exact rendered-child artifacts named by
// Subject. Paths are private runtime locators rather than authority: each evaluator must reopen
// the artifact it uses and prove its bytes against Subject before returning a result.
type SegmentScreeningMedia struct {
	Subject          SegmentScreeningSubject
	SourceMasterPath string
	EvidencePath     string
	PlaybackPath     string
}

// SegmentScreeningEvaluator owns one repeat-safe, authority-bound axis operation. A retry for the
// same subject must replay its closed result instead of repeating a possibly billed call.
type SegmentScreeningEvaluator interface {
	Axis() SegmentScreeningAxis
	Evaluate(context.Context, SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, error)
}

// SegmentScreeningRuntime is the post-render coordinator. It makes the immutable child subject
// durable first, then records exactly one result for each independent axis before publishing the
// aggregate. It does not interpret an axis result or turn a hold into a pass.
type SegmentScreeningRuntime struct {
	evaluators    []SegmentScreeningEvaluator
	evidence      SegmentScreeningEvidenceRepository
	airworthiness *fillerairworthiness.Evaluator
}

func NewSegmentScreeningRuntime(evaluators []SegmentScreeningEvaluator, evidence SegmentScreeningEvidenceRepository, airworthiness *fillerairworthiness.Evaluator) (*SegmentScreeningRuntime, error) {
	if len(evaluators) != len(segmentScreeningAxisOrder) || evidence == nil || airworthiness == nil {
		return nil, fmt.Errorf("segment screening runtime requires five evaluators, evidence repository, and Airworthiness policy")
	}
	byAxis := make(map[SegmentScreeningAxis]SegmentScreeningEvaluator, len(evaluators))
	for _, evaluator := range evaluators {
		if evaluator == nil {
			return nil, fmt.Errorf("segment screening runtime contains a nil evaluator")
		}
		axis := evaluator.Axis()
		if err := validateSegmentScreeningAxis(axis); err != nil {
			return nil, fmt.Errorf("segment screening runtime contains an unknown axis %q", axis)
		}
		if _, duplicate := byAxis[axis]; duplicate {
			return nil, fmt.Errorf("segment screening runtime repeats axis %q", axis)
		}
		byAxis[axis] = evaluator
	}
	ordered := make([]SegmentScreeningEvaluator, 0, len(segmentScreeningAxisOrder))
	for _, axis := range segmentScreeningAxisOrder {
		evaluator, exists := byAxis[axis]
		if !exists {
			return nil, fmt.Errorf("segment screening runtime is missing axis %q", axis)
		}
		ordered = append(ordered, evaluator)
	}
	return &SegmentScreeningRuntime{evaluators: ordered, evidence: evidence, airworthiness: airworthiness}, nil
}

func (r *SegmentScreeningRuntime) Screen(ctx context.Context, media SegmentScreeningMedia) (SegmentScreeningEvidence, error) {
	if r == nil || len(r.evaluators) != len(segmentScreeningAxisOrder) || r.evidence == nil || r.airworthiness == nil {
		return SegmentScreeningEvidence{}, fmt.Errorf("segment screening runtime is unavailable")
	}
	if err := validateSegmentScreeningMedia(media); err != nil {
		return SegmentScreeningEvidence{}, err
	}
	if err := ctx.Err(); err != nil {
		return SegmentScreeningEvidence{}, err
	}
	if err := r.evidence.PutSegmentScreeningSubject(ctx, media.Subject); err != nil {
		return SegmentScreeningEvidence{}, fmt.Errorf("persist segment screening subject: %w", err)
	}

	results := make([]SegmentScreeningResult, 0, len(r.evaluators))
	records := make([]RecordedSegmentScreeningAxisEvidence, 0, len(r.evaluators))
	var aggregateAssessedAt time.Time
	for index, evaluator := range r.evaluators {
		if err := ctx.Err(); err != nil {
			return SegmentScreeningEvidence{}, err
		}
		recorded, err := evaluator.Evaluate(ctx, media)
		if err != nil {
			return SegmentScreeningEvidence{}, fmt.Errorf("screen child axis %q produced no authority: %w", evaluator.Axis(), err)
		}
		if err := ValidateRecordedSegmentScreeningAxisEvidence(recorded); err != nil || recorded.Evidence.SubjectSHA256 != media.Subject.SHA256 {
			return SegmentScreeningEvidence{}, fmt.Errorf("screen child axis %q returned invalid or subject-drifted evidence", evaluator.Axis())
		}
		result := recorded.Evidence.Result()
		if result.Axis != segmentScreeningAxisOrder[index] || result.Axis != evaluator.Axis() || validateSegmentScreeningResult(result) != nil {
			return SegmentScreeningEvidence{}, fmt.Errorf("screen child axis %q returned invalid or axis-drifted evidence", evaluator.Axis())
		}
		if err := r.evidence.PutSegmentScreeningAxisEvidence(ctx, recorded); err != nil {
			return SegmentScreeningEvidence{}, fmt.Errorf("persist screen child axis %q: %w", evaluator.Axis(), err)
		}
		results = append(results, result)
		records = append(records, recorded)
		if recorded.Evidence.AssessedAt.After(aggregateAssessedAt) {
			aggregateAssessedAt = recorded.Evidence.AssessedAt
		}
	}
	airworthiness, err := evaluateSegmentAirworthiness(media.Subject, records, r.airworthiness)
	if err != nil {
		return SegmentScreeningEvidence{}, fmt.Errorf("evaluate child Airworthiness: %w", err)
	}
	aggregate, err := NewSegmentScreeningEvidence(media.Subject, results, airworthiness, aggregateAssessedAt)
	if err != nil {
		return SegmentScreeningEvidence{}, fmt.Errorf("assemble child screening: %w", err)
	}
	if err := r.evidence.PutSegmentScreeningEvidence(ctx, aggregate); err != nil {
		return SegmentScreeningEvidence{}, fmt.Errorf("persist child screening: %w", err)
	}
	return aggregate, nil
}

func validateSegmentScreeningMedia(media SegmentScreeningMedia) error {
	if err := ValidateSegmentScreeningSubject(media.Subject); err != nil {
		return fmt.Errorf("segment screening media subject: %w", err)
	}
	paths := []string{media.SourceMasterPath, media.EvidencePath, media.PlaybackPath}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("segment screening media requires clean absolute artifact paths")
		}
		if _, duplicate := seen[path]; duplicate {
			return fmt.Errorf("segment screening media artifacts must be distinct")
		}
		seen[path] = struct{}{}
	}
	return nil
}
