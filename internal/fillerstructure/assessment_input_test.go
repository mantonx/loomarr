package fillerstructure

import (
	"slices"
	"strings"
	"testing"
)

func TestAssessmentInputRepresentsCompleteVideoAndWindowSetExactly(t *testing.T) {
	source := Source{SHA256: strings.Repeat("a", 64), Bytes: 2_048, DurationMS: 300_000}
	completeMedia := AssessmentMedia{
		SHA256: strings.Repeat("b", 64), Bytes: 1_024, DurationMS: 300_000,
		ProfileSHA256: strings.Repeat("c", 64), LineageSHA256: strings.Repeat("d", 64),
	}
	complete, err := NewCompleteVideoInput(source, completeMedia)
	if err != nil {
		t.Fatal(err)
	}
	windows := []AssessmentMedia{
		{SHA256: strings.Repeat("1", 64), Bytes: 1_000, DurationMS: 135_000, ProfileSHA256: strings.Repeat("c", 64), LineageSHA256: strings.Repeat("4", 64)},
		{SHA256: strings.Repeat("2", 64), Bytes: 1_100, DurationMS: 150_000, ProfileSHA256: strings.Repeat("c", 64), LineageSHA256: strings.Repeat("5", 64)},
		{SHA256: strings.Repeat("3", 64), Bytes: 1_200, DurationMS: 75_000, ProfileSHA256: strings.Repeat("c", 64), LineageSHA256: strings.Repeat("6", 64)},
	}
	windowed, err := NewWindowMediaSetInput(source, strings.Repeat("e", 64), windows)
	if err != nil {
		t.Fatal(err)
	}
	if complete.Kind != AssessmentInputCompleteVideo || len(complete.Items) != 1 || complete.PlanSHA256 != "" ||
		windowed.Kind != AssessmentInputWindowMediaSet || len(windowed.Items) != 3 || windowed.PlanSHA256 == "" ||
		complete.SHA256 == windowed.SHA256 || ValidateAssessmentInput(complete) != nil || ValidateAssessmentInput(windowed) != nil {
		t.Fatalf("complete=%+v windowed=%+v", complete, windowed)
	}
}

func TestAssessmentInputRejectsRehashedSemanticDrift(t *testing.T) {
	request := fixtureRequest()
	valid := request.Input
	tests := []struct {
		name   string
		mutate func(*AssessmentInput)
	}{
		{name: "source", mutate: func(input *AssessmentInput) { input.Source.DurationMS += 1_001 }},
		{name: "profile", mutate: func(input *AssessmentInput) { input.ProfileSHA256 = strings.Repeat("a", 64) }},
		{name: "item profile", mutate: func(input *AssessmentInput) { input.Items[0].ProfileSHA256 = strings.Repeat("a", 64) }},
		{name: "item lineage", mutate: func(input *AssessmentInput) { input.Items[0].LineageSHA256 = "" }},
		{name: "complete plan", mutate: func(input *AssessmentInput) { input.PlanSHA256 = strings.Repeat("a", 64) }},
		{name: "complete item count", mutate: func(input *AssessmentInput) { input.Items = append(input.Items, input.Items[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			input.Items = slices.Clone(valid.Items)
			test.mutate(&input)
			input.SHA256 = AssessmentInputSHA256(input)
			if err := ValidateAssessmentInput(input); err == nil {
				t.Fatal("rehashed assessment input drift was accepted")
			}
		})
	}
}

func TestNewCandidateDerivesClaimsAndRejectsDeclaredIdentityDrift(t *testing.T) {
	request := fixtureRequest()
	candidate, err := NewCandidate(request.Source, request.Input.SHA256, request.Candidates[0].Assessor, "", []Segment{
		{StartMS: 0, EndMS: 5_000, Role: RoleCommercial},
		{StartMS: 5_000, EndMS: 10_000, Role: RoleCommercial},
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Unit != UnitCompilation || candidate.Role != "" || len(candidate.Segments) != 2 {
		t.Fatalf("candidate = %+v", candidate)
	}
	if _, err := NewCandidate(request.Source, strings.Repeat("x", 64), request.Candidates[0].Assessor, "", candidate.Segments); err == nil {
		t.Fatal("invalid input identity was accepted")
	}
	if _, err := NewCandidate(request.Source, request.Input.SHA256, request.Candidates[0].Assessor, " timeout ", nil); err == nil {
		t.Fatal("non-canonical failure was normalized and accepted")
	}
}
