package fillerstructure

import (
	"slices"
	"strings"
	"testing"
)

func TestReduceConfirmsOnlyIndependentCompleteTimelineAgreement(t *testing.T) {
	request := fixtureRequest()
	request.Candidates[0].Segments[0].EndMS = 4_000
	request.Candidates[0].Segments[1].StartMS = 4_000
	request.Candidates[1].Segments[0].EndMS = 5_000
	request.Candidates[1].Segments[1].StartMS = 5_000
	decision := Reduce(request)
	if decision.Status != StatusConfirmed || decision.Unit != UnitCompilation || len(decision.Segments) != 2 || decision.Segments[0].EndMS != 4_500 || decision.Segments[0].Disposition != DispositionFillerCandidate || decision.Segments[1].Role != RolePromo || !slices.Equal(decision.ReasonCodes, []string{ReasonAgreement}) {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestReduceInvalidInputHoldsWithoutPanicking(t *testing.T) {
	decision := Reduce(Request{})
	if decision.Status != StatusHeld || !slices.Equal(decision.ReasonCodes, []string{ReasonInvalidCandidate}) || len(decision.Segments) != 0 {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestReduceHoldsRatherThanVotingAcrossConflicts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
		reason string
	}{
		{name: "same family", mutate: func(request *Request) { request.Candidates[1].Assessor.ModelFamily = "family-a" }, reason: ReasonInvalidCandidate},
		{name: "wrong source", mutate: func(request *Request) { request.Candidates[1].Source.SHA256 = strings.Repeat("c", 64) }, reason: ReasonInvalidCandidate},
		{name: "wrong input", mutate: func(request *Request) { request.Candidates[1].InputSHA256 = strings.Repeat("d", 64) }, reason: ReasonInvalidCandidate},
		{name: "operational failure", mutate: func(request *Request) {
			request.Candidates[1].Failure, request.Candidates[1].Unit, request.Candidates[1].Segments = "timeout", "", nil
		}, reason: ReasonOperationalFailure},
		{name: "unit", mutate: func(request *Request) { request.Candidates[1].Unit = UnitProgrammeSpots }, reason: ReasonUnitDisagreement},
		{name: "role", mutate: func(request *Request) { request.Candidates[1].Segments[0].Role = RolePSA }, reason: ReasonIntervalRole},
		{name: "count", mutate: func(request *Request) {
			request.Candidates[1].Segments = []Segment{{StartMS: 0, EndMS: 10_000, Role: RoleCommercial}}
		}, reason: ReasonIntervalCount},
		{name: "boundary", mutate: func(request *Request) {
			request.Candidates[1].Segments[0].EndMS = 7_001
			request.Candidates[1].Segments[1].StartMS = 7_001
		}, reason: ReasonBoundary},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := fixtureRequest()
			test.mutate(&request)
			decision := Reduce(request)
			if decision.Status != StatusHeld || !slices.Contains(decision.ReasonCodes, test.reason) || len(decision.Segments) != 0 {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestReduceGivesOneBoundaryVotePerModelFamily(t *testing.T) {
	request := fixtureRequest()
	duplicate := request.Candidates[0]
	duplicate.Segments = slices.Clone(duplicate.Segments)
	duplicate.Assessor.ID = "assessor-c"
	duplicate.Assessor.AssessmentSHA256 = strings.Repeat("d", 64)
	duplicate.Segments[0].EndMS = 6_000
	duplicate.Segments[1].StartMS = 6_000
	request.Candidates = append(request.Candidates, duplicate)
	decision := Reduce(request)
	if decision.Status != StatusConfirmed || decision.Segments[0].EndMS != 5_250 {
		t.Fatalf("same-family route became an extra vote: %+v", decision)
	}
}

func fixtureRequest() Request {
	source := Source{SHA256: strings.Repeat("a", 64), Bytes: 2_048, DurationMS: 10_000}
	media := AssessmentMedia{SHA256: strings.Repeat("e", 64), Bytes: 1_024, DurationMS: 10_000, ProfileSHA256: strings.Repeat("f", 64), LineageSHA256: strings.Repeat("d", 64)}
	input, err := NewCompleteVideoInput(source, media)
	if err != nil {
		panic(err)
	}
	candidate := func(id, family, digest string) Candidate {
		return Candidate{
			Source: source, InputSHA256: input.SHA256,
			Assessor: Assessor{
				ID: id, ModelFamily: family, Provider: "provider", Model: "model",
				ModelDigest: strings.Repeat("b", 64), CapabilitySHA256: strings.Repeat("c", 64),
				PromptVersion: "prompt-v1", EvidenceContract: "assessment-v1",
				AssessmentSHA256: strings.Repeat(digest, 64),
			},
			Unit:     UnitCompilation,
			Segments: []Segment{{StartMS: 0, EndMS: 5_000, Role: RoleCommercial}, {StartMS: 5_000, EndMS: 10_000, Role: RolePromo}},
		}
	}
	return Request{Source: source, Input: input, BoundaryToleranceMS: 2_000, Candidates: []Candidate{candidate("assessor-a", "family-a", "1"), candidate("assessor-b", "family-b", "2")}}
}
