package fillerstructure

import (
	"errors"
	"slices"
	"strings"
)

// NewCandidate closes one assessor's complete source-level answer. Unit and standalone role are
// derived from the timeline here so full-video and windowed adapters cannot project differently.
func NewCandidate(source Source, inputSHA256 string, assessor Assessor, failure string, segments []Segment) (Candidate, error) {
	trimmedFailure := strings.TrimSpace(failure)
	candidate := Candidate{
		Source: source, InputSHA256: inputSHA256, Assessor: assessor,
		Failure: trimmedFailure, Segments: slices.Clone(segments),
	}
	if !validSource(source) || !digest(inputSHA256) || !validAssessor(assessor) || failure != trimmedFailure {
		return Candidate{}, errors.New("filler structure candidate identity is invalid")
	}
	if candidate.Failure != "" {
		if len(candidate.Failure) > 64 || len(candidate.Segments) != 0 {
			return Candidate{}, errors.New("filler structure failed candidate is invalid")
		}
		return candidate, nil
	}
	if !completeTimeline(candidate.Segments, source.DurationMS) {
		return Candidate{}, errors.New("filler structure candidate timeline is incomplete")
	}
	candidate.Unit, candidate.Role = claimsForTimeline(candidate.Segments)
	return candidate, nil
}

func claimsForTimeline(segments []Segment) (Unit, Role) {
	if len(segments) == 1 {
		switch role := segments[0].Role; {
		case fillerRole(role):
			return UnitStandalone, role
		case role == RoleProgrammeFragment:
			return UnitProgrammeExcerpt, ""
		case role == RoleUnusable:
			return UnitUnusable, ""
		default:
			return UnitUnclear, ""
		}
	}
	programme, filler, unsupported := 0, 0, 0
	for _, segment := range segments {
		switch {
		case segment.Role == RoleProgrammeFragment:
			programme++
		case fillerRole(segment.Role):
			filler++
		default:
			unsupported++
		}
	}
	if programme >= 2 && filler >= 1 && unsupported == 0 &&
		segments[0].Role == RoleProgrammeFragment && segments[len(segments)-1].Role == RoleProgrammeFragment {
		return UnitProgrammeSpots, ""
	}
	if programme == 0 {
		return UnitCompilation, ""
	}
	return UnitUnclear, ""
}

func validAssessor(assessor Assessor) bool {
	return validProfile(Profile(assessor)) && digest(assessor.AssessmentSHA256)
}
