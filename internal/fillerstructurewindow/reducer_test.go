package fillerstructurewindow

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestReducerCandidateProjectsOneCompleteFamilyWithoutInventingVideo(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	timeline := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	stitch, err := Stitch(set, assessmentsForTimeline(t, set, timeline), 2_000)
	if err != nil {
		t.Fatal(err)
	}
	input, candidate, err := ReducerCandidate(stitch)
	if err != nil {
		t.Fatal(err)
	}
	if input.Kind != fillerstructure.AssessmentInputWindowMediaSet || input.PlanSHA256 != plan.SHA256 ||
		len(input.Items) != len(set.Windows) || candidate.InputSHA256 != input.SHA256 ||
		candidate.Assessor.AssessmentSHA256 != stitch.SHA256 || candidate.Unit != fillerstructure.UnitCompilation ||
		!reflect.DeepEqual(candidate.Segments, timeline) {
		t.Fatalf("input=%+v candidate=%+v", input, candidate)
	}
	if input.SHA256 == set.Windows[0].Media.SHA256 {
		t.Fatal("window media set was represented as one constituent video")
	}
}

func TestTwoStitchedFamiliesEnterSharedReducerAsTwoVotes(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	timeline := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	left, err := Stitch(set, assessmentsForFamily(t, set, timeline, "assessor-a", "family-a", "a"), 2_000)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Stitch(set, assessmentsForFamily(t, set, timeline, "assessor-b", "family-b", "b"), 2_000)
	if err != nil {
		t.Fatal(err)
	}
	input, leftCandidate, err := ReducerCandidate(left)
	if err != nil {
		t.Fatal(err)
	}
	rightInput, rightCandidate, err := ReducerCandidate(right)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, rightInput) {
		t.Fatal("independent families did not bind the same assessment input")
	}
	decision := fillerstructure.Reduce(fillerstructure.Request{
		Source: plan.Source, Input: input, BoundaryToleranceMS: 2_000,
		Candidates: []fillerstructure.Candidate{leftCandidate, rightCandidate},
	})
	if decision.Status != fillerstructure.StatusConfirmed || decision.Unit != fillerstructure.UnitCompilation ||
		len(decision.Segments) != 2 || decision.Segments[0].EndMS != 120_000 {
		t.Fatalf("windowed decision = %+v", decision)
	}
}

func TestHeldStitchBecomesOneOperationalCandidate(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	assessments := assessmentsForFamily(t, set, []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}, "assessor-a", "family-a", "a")
	failed := assessmentInputFixture(set, 1, nil)
	failed.Assessor = assessments[0].Assessor
	failed.Failure = "provider_timeout"
	assessments[1], err = NewAssessment(failed)
	if err != nil {
		t.Fatal(err)
	}
	stitch, err := Stitch(set, assessments, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	_, candidate, err := ReducerCandidate(stitch)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Failure != HoldOperationalFailure || candidate.Unit != "" || len(candidate.Segments) != 0 {
		t.Fatalf("held candidate = %+v", candidate)
	}
}

func assessmentsForFamily(t *testing.T, set MediaSet, timeline []fillerstructure.Segment, id, family, digest string) []Assessment {
	t.Helper()
	assessments := make([]Assessment, len(set.Plan.Windows))
	for ordinal, window := range set.Plan.Windows {
		input := assessmentInputFixture(set, ordinal, clipTimeline(timeline, window))
		input.Assessor.ID, input.Assessor.ModelFamily = id, family
		input.Assessor.ModelDigest = strings.Repeat(digest, 64)
		input.AssessedAt = time.Date(2026, 9, 4, 12, 0, ordinal, 0, time.UTC)
		assessment, err := NewAssessment(input)
		if err != nil {
			t.Fatal(err)
		}
		assessments[ordinal] = assessment
	}
	return assessments
}
