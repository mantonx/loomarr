package fillerstructure

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestDirectVideoPromptAndSchemaIdentityAreStableAndDurationBound(t *testing.T) {
	wantDigest := regexp.MustCompile(`^[0-9a-f]{64}$`)
	prompt := DirectVideoPromptSHA256(1_000)
	schema := DirectVideoSchemaSHA256(1_000)
	if !wantDigest.MatchString(prompt) || !wantDigest.MatchString(schema) ||
		prompt != DirectVideoPromptSHA256(1_000) || schema != DirectVideoSchemaSHA256(1_000) ||
		prompt == DirectVideoPromptSHA256(2_000) || schema == DirectVideoSchemaSHA256(2_000) {
		t.Fatalf("prompt=%q schema=%q", prompt, schema)
	}
}

func TestParseDirectVideoResponseNormalizesAndDerivesProgrammeSpots(t *testing.T) {
	raw := `{"segments":[{"endMs":4500,"role":"programme_fragment","decisiveAtMs":[4000,1000],"reason":"title"},{"endMs":20000,"role":"programme_fragment","decisiveAtMs":[15000],"reason":"opening"},{"endMs":80000,"role":"commercial","decisiveAtMs":[70000,25000],"reason":"offer"},{"endMs":99555,"role":"programme_fragment","decisiveAtMs":[98000],"reason":"resumes"}]}`
	response, assessment, err := ParseDirectVideoResponse(raw, 99_555)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Segments) != 3 || response.Segments[0].EndMS != 20_000 || assessment.Unit.Kind != string(UnitProgrammeSpots) || !slices.Equal(assessment.Unit.DecisiveAtMS, []int64{20_000, 80_000}) || assessment.Segments[1].StartMS != 20_000 {
		t.Fatalf("response=%+v assessment=%+v", response, assessment)
	}
	fixture := fixtureRequest()
	candidate, err := DirectVideoCandidate(Source{SHA256: strings.Repeat("a", 64), Bytes: 2_048, DurationMS: 99_555}, AssessmentMedia{SHA256: strings.Repeat("e", 64), Bytes: 1_024, DurationMS: 99_555, ProfileSHA256: strings.Repeat("f", 64), LineageSHA256: strings.Repeat("d", 64)}, fixture.Candidates[0].Assessor, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Unit != UnitProgrammeSpots || len(candidate.Segments) != 3 || candidate.Segments[1].Role != RoleCommercial {
		t.Fatalf("candidate=%+v", candidate)
	}
}

func TestParseDirectVideoResponsePreservesAdjacentFiller(t *testing.T) {
	raw := `{"segments":[{"endMs":500,"role":"commercial","decisiveAtMs":[200],"reason":"first"},{"endMs":1000,"role":"commercial","decisiveAtMs":[700],"reason":"second"}]}`
	response, assessment, err := ParseDirectVideoResponse(raw, 1_000)
	if err != nil || len(response.Segments) != 2 || assessment.Unit.Kind != string(UnitCompilation) {
		t.Fatalf("response=%+v assessment=%+v err=%v", response, assessment, err)
	}
}

func TestParseDirectVideoResponseRejectsIncompleteOrUnresolvedEvidence(t *testing.T) {
	tests := []string{
		`{"segments":[{"endMs":999,"role":"commercial","decisiveAtMs":[200],"reason":"offer"}]}`,
		`{"segments":[{"endMs":1000,"role":"commercial","decisiveAtMs":[],"reason":"offer"}]}`,
		`{"segments":[{"endMs":1000,"role":"commercial","decisiveAtMs":[1001],"reason":"offer"}]}`,
		`{"segments":[{"endMs":1000,"role":"commercial","decisiveAtMs":[200],"reason":"offer","extra":true}]}`,
	}
	for _, raw := range tests {
		if _, _, err := ParseDirectVideoResponse(raw, 1_000); err == nil {
			t.Fatalf("invalid response accepted: %s", raw)
		}
	}
}

func TestAssessDirectVideoResponseEnforcesLocalSegmentCeiling(t *testing.T) {
	t.Parallel()
	response := DirectVideoResponse{Segments: make([]DirectVideoResponseSegment, DirectVideoMaximumSegments+1)}
	for index := range response.Segments {
		response.Segments[index] = DirectVideoResponseSegment{
			EndMS: int64(index + 1), Role: string(RoleCommercial),
			DecisiveAtMS: []int64{int64(index)}, Reason: "bounded item",
		}
	}
	if _, err := AssessDirectVideoResponse(response, int64(len(response.Segments))); err == nil {
		t.Fatal("assessment accepted more than the local segment ceiling")
	}
}
