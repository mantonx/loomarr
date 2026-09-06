package filler

import (
	"fmt"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
)

// NewSegmentAirworthinessEvaluator binds one audience profile to the exact
// three safety-axis authorities selected for rendered-child screening.
func NewSegmentAirworthinessEvaluator(profile fillerairworthiness.Profile, profiles []SegmentScreeningAxisProfile) (*fillerairworthiness.Evaluator, error) {
	axisProfiles := make([]fillerairworthiness.AxisProfile, 0, 3)
	for _, candidate := range profiles {
		if _, safety := airworthinessAxis(candidate.Axis); !safety {
			continue
		}
		axisProfile, err := segmentAirworthinessProfile(candidate)
		if err != nil {
			return nil, err
		}
		axisProfiles = append(axisProfiles, axisProfile)
	}
	return fillerairworthiness.New(profile, axisProfiles)
}

func evaluateSegmentAirworthiness(subject SegmentScreeningSubject, records []RecordedSegmentScreeningAxisEvidence, evaluator *fillerairworthiness.Evaluator) (fillerairworthiness.Decision, error) {
	if ValidateSegmentScreeningSubject(subject) != nil || evaluator == nil {
		return fillerairworthiness.Decision{}, fmt.Errorf("segment Airworthiness evaluator or subject is invalid")
	}
	document := fillerairworthiness.Document{
		SchemaVersion:   fillerairworthiness.EvidenceSchemaVersion,
		ContractVersion: fillerairworthiness.EvidenceContractVersion,
		SubjectSHA256:   subject.SHA256, DurationMS: subject.EvidenceDurationMs,
	}
	seen := make(map[SegmentScreeningAxis]struct{}, 3)
	for _, recorded := range records {
		axis := recorded.Evidence.Profile.Axis
		if _, safety := airworthinessAxis(axis); !safety {
			continue
		}
		if _, duplicate := seen[axis]; duplicate || ValidateRecordedSegmentScreeningAxisEvidence(recorded) != nil ||
			recorded.Evidence.SubjectSHA256 != subject.SHA256 || recorded.Evidence.Suitability == nil {
			return fillerairworthiness.Decision{}, fmt.Errorf("segment Airworthiness input is invalid or repeated")
		}
		seen[axis] = struct{}{}
		document.Axes = append(document.Axes, *cloneSuitabilityAxisEvidence(recorded.Evidence.Suitability))
	}
	if len(seen) != 3 {
		return fillerairworthiness.Decision{}, fmt.Errorf("segment Airworthiness requires three safety axes")
	}
	decision := evaluator.Evaluate(document)
	if err := fillerairworthiness.ValidateDecision(decision); err != nil {
		return fillerairworthiness.Decision{}, fmt.Errorf("segment Airworthiness decision: %w", err)
	}
	return decision, nil
}

func segmentAirworthinessMatches(subject SegmentScreeningSubject, records []RecordedSegmentScreeningAxisEvidence, evaluator *fillerairworthiness.Evaluator, want fillerairworthiness.Decision) bool {
	decision, err := evaluateSegmentAirworthiness(subject, records, evaluator)
	return err == nil && reflect.DeepEqual(decision, want)
}
