package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	StitchSchemaVersion   = 2
	StitchContractVersion = "filler-structure-window-stitch-v2"

	HoldOperationalFailure = "window_operational_failure"
	HoldOverlapConflict    = "window_overlap_conflict"
	HoldTimelineConflict   = "window_timeline_conflict"
)

type StitchStatus string

const (
	StitchComplete StitchStatus = "complete"
	StitchHeld     StitchStatus = "held"
)

// StitchResult retains every window answer and deterministically projects them into exactly one
// complete source timeline for one assessor family. It is not an independent-family decision.
type StitchResult struct {
	SchemaVersion       int                             `json:"schemaVersion"`
	ContractVersion     string                          `json:"contractVersion"`
	MediaSet            MediaSet                        `json:"mediaSet"`
	Assessor            fillerstructure.AssessorProfile `json:"assessor"`
	BoundaryToleranceMS int64                           `json:"boundaryToleranceMs"`
	Assessments         []Assessment                    `json:"assessments"`
	Status              StitchStatus                    `json:"status"`
	HoldReason          string                          `json:"holdReason,omitempty"`
	Segments            []fillerstructure.Segment       `json:"segments,omitempty"`
	SHA256              string                          `json:"sha256"`
}

// Stitch builds one replayable family result. Missing window authority is an error; an explicit
// operational-failure assessment is a durable held result.
func Stitch(set MediaSet, assessments []Assessment, boundaryToleranceMS int64) (StitchResult, error) {
	result, err := buildStitch(set, assessments, boundaryToleranceMS)
	if err != nil {
		return StitchResult{}, err
	}
	result.SHA256 = StitchSHA256(result)
	if err := ValidateStitchResult(result); err != nil {
		return StitchResult{}, err
	}
	return result, nil
}

// StitchSHA256 returns the stitched artifact identity with its self-digest excluded.
func StitchSHA256(result StitchResult) string {
	result.SHA256 = ""
	raw, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// ValidateStitchResult replays ordering, overlap comparison, and timeline projection.
func ValidateStitchResult(result StitchResult) error {
	if !contentHash(result.SHA256) || result.SHA256 != StitchSHA256(result) {
		return errors.New("structure window stitch identity is invalid")
	}
	want, err := buildStitch(result.MediaSet, result.Assessments, result.BoundaryToleranceMS)
	if err != nil {
		return err
	}
	want.SHA256 = result.SHA256
	if !reflect.DeepEqual(result, want) {
		return errors.New("structure window stitch result does not reproduce")
	}
	return nil
}

func buildStitch(set MediaSet, assessments []Assessment, boundaryToleranceMS int64) (StitchResult, error) {
	if err := ValidateMediaSet(set); err != nil {
		return StitchResult{}, err
	}
	plan := set.Plan
	if boundaryToleranceMS < 0 || boundaryToleranceMS >= plan.Profile.ContextOverlapMS {
		return StitchResult{}, errors.New("structure window boundary tolerance is invalid")
	}
	ordered := slices.Clone(assessments)
	slices.SortFunc(ordered, func(left, right Assessment) int { return left.WindowOrdinal - right.WindowOrdinal })
	if len(ordered) != len(plan.Windows) {
		return StitchResult{}, errors.New("structure window stitch lacks complete assessment authority")
	}
	var assessor fillerstructure.AssessorProfile
	failed := false
	for index, assessment := range ordered {
		if assessment.WindowOrdinal != index {
			return StitchResult{}, errors.New("structure window stitch repeats or omits a window")
		}
		if err := ValidateAssessment(set, assessment); err != nil {
			return StitchResult{}, err
		}
		if index == 0 {
			assessor = assessment.Assessor
		} else if assessment.Assessor != assessor {
			return StitchResult{}, errors.New("structure window stitch mixes assessor profiles")
		}
		failed = failed || assessment.State == AssessmentOperationalFailure
	}
	result := StitchResult{
		SchemaVersion: StitchSchemaVersion, ContractVersion: StitchContractVersion,
		MediaSet: set, Assessor: assessor, BoundaryToleranceMS: boundaryToleranceMS, Assessments: ordered,
	}
	if failed {
		result.Status, result.HoldReason = StitchHeld, HoldOperationalFailure
		return result, nil
	}
	segments, hold := stitchAcceptedWindows(plan, ordered, boundaryToleranceMS)
	if hold != "" {
		result.Status, result.HoldReason = StitchHeld, hold
		return result, nil
	}
	result.Status, result.Segments = StitchComplete, segments
	return result, nil
}
