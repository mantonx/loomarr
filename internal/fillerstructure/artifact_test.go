package fillerstructure

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestArtifactRetainsAndReplaysIndependentDecision(t *testing.T) {
	decidedAt := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	artifact, err := NewArtifact(fixtureRequest(), decidedAt)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Decision.Status != StatusConfirmed || artifact.DecidedAt != decidedAt || artifact.SHA256 == "" || ValidateArtifact(artifact) != nil {
		t.Fatalf("artifact=%+v", artifact)
	}
	if len(artifact.Decision.Candidates) != 2 || artifact.Decision.Candidates[0].Assessor.AssessmentSHA256 == "" {
		t.Fatalf("candidate identities were not retained: %+v", artifact.Decision.Candidates)
	}
}

func TestArtifactRetainsOperationalFailureAsHold(t *testing.T) {
	request := fixtureRequest()
	request.Candidates[1].Failure, request.Candidates[1].Unit, request.Candidates[1].Segments = "timeout", "", nil
	artifact, err := NewArtifact(request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Decision.Status != StatusHeld || !slices.Equal(artifact.Decision.ReasonCodes, []string{ReasonOperationalFailure}) || ValidateArtifact(artifact) != nil {
		t.Fatalf("artifact=%+v", artifact)
	}
}

func TestArtifactRejectsInvalidCandidatesAndMutation(t *testing.T) {
	if _, err := NewArtifact(Request{}, time.Now()); err == nil {
		t.Fatal("invalid candidates created an artifact")
	}
	artifact, err := NewArtifact(fixtureRequest(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Artifact)
	}{
		{name: "decision", mutate: func(a *Artifact) { a.Decision.Segments[0].EndMS++ }},
		{name: "candidate", mutate: func(a *Artifact) { a.Decision.Candidates[0].Segments[0].EndMS++ }},
		{name: "source", mutate: func(a *Artifact) { a.Decision.Source.SHA256 = strings.Repeat("f", 64) }},
		{name: "input", mutate: func(a *Artifact) { a.Decision.Input.Items[0].SHA256 = strings.Repeat("f", 64) }},
		{name: "policy", mutate: func(a *Artifact) { a.BoundaryToleranceMS = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := artifact
			candidate.Decision.Segments = slices.Clone(artifact.Decision.Segments)
			candidate.Decision.Candidates = cloneCandidates(artifact.Decision.Candidates)
			candidate.Decision.Input.Items = slices.Clone(artifact.Decision.Input.Items)
			test.mutate(&candidate)
			candidate.SHA256 = ArtifactSHA256(candidate)
			if err := ValidateArtifact(candidate); err == nil {
				t.Fatal("rehashed mutation was accepted")
			}
		})
	}
}

func cloneCandidates(candidates []Candidate) []Candidate {
	result := slices.Clone(candidates)
	for index := range result {
		result[index].Segments = slices.Clone(result[index].Segments)
	}
	return result
}
