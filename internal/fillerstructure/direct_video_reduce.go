package fillerstructure

import (
	"errors"
	"slices"
	"strings"
)

func NormalizeDirectVideoResponse(response *DirectVideoResponse) {
	for index := range response.Segments {
		slices.Sort(response.Segments[index].DecisiveAtMS)
	}
	response.Segments = coalesceDirectVideoProgramme(response.Segments)
}

func coalesceDirectVideoProgramme(segments []DirectVideoResponseSegment) []DirectVideoResponseSegment {
	result := make([]DirectVideoResponseSegment, 0, len(segments))
	for _, segment := range segments {
		if len(result) == 0 {
			result = append(result, segment)
			continue
		}
		previous := &result[len(result)-1]
		if previous.Role != string(RoleProgrammeFragment) || segment.Role != previous.Role || segment.EndMS <= previous.EndMS {
			result = append(result, segment)
			continue
		}
		previous.EndMS = segment.EndMS
		previous.DecisiveAtMS = BoundedDirectVideoTimes(append(slices.Clone(previous.DecisiveAtMS), segment.DecisiveAtMS...))
		previous.Reason = "adjacent programme observations form one continuous programme interval"
	}
	return result
}

func BoundedDirectVideoTimes(values []int64) []int64 {
	slices.Sort(values)
	values = slices.Compact(values)
	if len(values) <= DirectVideoMaximumDecisiveTime {
		return values
	}
	bounded := make([]int64, 0, DirectVideoMaximumDecisiveTime)
	for index := range DirectVideoMaximumDecisiveTime {
		at := index * (len(values) - 1) / (DirectVideoMaximumDecisiveTime - 1)
		bounded = append(bounded, values[at])
	}
	return bounded
}

func AssessDirectVideoResponse(response DirectVideoResponse, durationMS int64) (DirectVideoAssessment, error) {
	assessment := DirectVideoAssessment{Segments: make([]DirectVideoAssessmentSegment, 0, len(response.Segments))}
	startMS := int64(0)
	for _, segment := range response.Segments {
		assessment.Segments = append(assessment.Segments, DirectVideoAssessmentSegment{
			StartMS: startMS, EndMS: segment.EndMS, Role: Role(segment.Role),
			DecisiveAtMS: slices.Clone(segment.DecisiveAtMS), Reason: strings.TrimSpace(segment.Reason),
		})
		startMS = segment.EndMS
	}
	assessment.Unit, assessment.Role = deriveDirectVideoClaims(assessment.Segments)
	return assessment, validateDirectVideoAssessment(assessment, durationMS)
}

// DirectVideoCandidate projects the shared parsed assessment into the reducer input after the
// adapter has persisted the response and supplied its complete immutable assessor identity.
func DirectVideoCandidate(source Source, media AssessmentMedia, assessor Assessor, assessment DirectVideoAssessment) (Candidate, error) {
	input, err := NewCompleteVideoInput(source, media)
	if err != nil {
		return Candidate{}, err
	}
	segments := make([]Segment, 0, len(assessment.Segments))
	for _, segment := range assessment.Segments {
		segments = append(segments, Segment{
			StartMS: segment.StartMS, EndMS: segment.EndMS, Role: segment.Role,
		})
	}
	candidate, err := NewCandidate(source, input.SHA256, assessor, "", segments)
	if err != nil {
		return Candidate{}, err
	}
	wantRole := Role("")
	if assessment.Role != nil {
		wantRole = Role(assessment.Role.Kind)
	}
	if candidate.Unit != Unit(assessment.Unit.Kind) || candidate.Role != wantRole {
		return Candidate{}, errors.New("direct-video candidate claims do not reproduce from timeline")
	}
	return candidate, nil
}

func deriveDirectVideoClaims(segments []DirectVideoAssessmentSegment) (DirectVideoClaim, *DirectVideoClaim) {
	if len(segments) == 0 {
		return DirectVideoClaim{Kind: string(UnitUnclear), Reason: "no complete segment timeline was returned"}, nil
	}
	if len(segments) == 1 {
		segment, evidence := segments[0], slices.Clone(segments[0].DecisiveAtMS)
		switch {
		case fillerRole(segment.Role):
			return DirectVideoClaim{Kind: string(UnitStandalone), DecisiveAtMS: evidence, Reason: "one independently bounded filler interval covers the complete video"}, &DirectVideoClaim{Kind: string(segment.Role), DecisiveAtMS: slices.Clone(evidence), Reason: segment.Reason}
		case segment.Role == RoleProgrammeFragment:
			return DirectVideoClaim{Kind: string(UnitProgrammeExcerpt), DecisiveAtMS: evidence, Reason: "one programme fragment covers the complete video"}, nil
		case segment.Role == RoleUnusable:
			return DirectVideoClaim{Kind: string(UnitUnusable), DecisiveAtMS: evidence, Reason: segment.Reason}, nil
		default:
			return DirectVideoClaim{Kind: string(UnitUnclear), DecisiveAtMS: evidence, Reason: "one whole-video interval does not establish a filler or programme unit"}, nil
		}
	}
	boundaries := make([]int64, 0, min(len(segments)-1, DirectVideoMaximumDecisiveTime))
	for index := 1; index < len(segments) && len(boundaries) < DirectVideoMaximumDecisiveTime; index++ {
		boundaries = append(boundaries, segments[index].StartMS)
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
	if programme >= 2 && filler >= 1 && unsupported == 0 && segments[0].Role == RoleProgrammeFragment && segments[len(segments)-1].Role == RoleProgrammeFragment {
		return DirectVideoClaim{Kind: string(UnitProgrammeSpots), DecisiveAtMS: boundaries, Reason: "programme fragments surround independently bounded filler intervals"}, nil
	}
	if programme == 0 {
		return DirectVideoClaim{Kind: string(UnitCompilation), DecisiveAtMS: boundaries, Reason: "multiple independently bounded non-programme intervals cover the complete video"}, nil
	}
	return DirectVideoClaim{Kind: string(UnitUnclear), DecisiveAtMS: boundaries, Reason: "the mixed segment timeline does not establish programme surrounding inserted filler"}, nil
}
