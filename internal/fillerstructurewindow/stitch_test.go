package fillerstructurewindow

import (
	"reflect"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestStitchProducesOneTimelineAndPreservesSameRoleSeam(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	truth := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 240_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 240_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	result, err := Stitch(set, assessmentsForTimeline(t, set, truth), 2_000)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StitchComplete || result.HoldReason != "" || !reflect.DeepEqual(result.Segments, truth) ||
		len(result.Assessments) != len(plan.Windows) || result.SHA256 != StitchSHA256(result) {
		t.Fatalf("stitch result = %+v", result)
	}
	if err := ValidateStitchResult(result); err != nil {
		t.Fatal(err)
	}
}

func TestStitchReconcilesSameFamilyOverlapObservationWithinTolerance(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	assessments := make([]Assessment, len(plan.Windows))
	assessments[0] = newWindowAssessment(t, set, 0, []fillerstructure.Segment{
		{StartMS: 0, EndMS: 119_500, Role: fillerstructure.RoleCommercial},
		{StartMS: 119_500, EndMS: 135_000, Role: fillerstructure.RolePromo},
	})
	assessments[1] = newWindowAssessment(t, set, 1, []fillerstructure.Segment{
		{StartMS: 105_000, EndMS: 120_500, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_500, EndMS: 255_000, Role: fillerstructure.RolePromo},
	})
	assessments[2] = newWindowAssessment(t, set, 2, []fillerstructure.Segment{
		{StartMS: 225_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	})
	result, err := Stitch(set, assessments, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	want := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	if result.Status != StitchComplete || !reflect.DeepEqual(result.Segments, want) {
		t.Fatalf("reconciled stitch = %+v, want %+v", result, want)
	}
}

func TestStitchHoldsWholeFamilyOnOverlapConflict(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	truth := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	tests := []struct {
		name   string
		mutate func(*testing.T, Plan, []Assessment)
	}{
		{name: "missing boundary", mutate: func(t *testing.T, plan Plan, assessments []Assessment) {
			assessments[1] = newWindowAssessment(t, set, 1, []fillerstructure.Segment{
				{StartMS: 105_000, EndMS: 255_000, Role: fillerstructure.RoleCommercial},
			})
		}},
		{name: "role conflict", mutate: func(t *testing.T, plan Plan, assessments []Assessment) {
			assessments[1] = newWindowAssessment(t, set, 1, []fillerstructure.Segment{
				{StartMS: 105_000, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
				{StartMS: 120_000, EndMS: 255_000, Role: fillerstructure.RoleTrailer},
			})
		}},
		{name: "outside tolerance", mutate: func(t *testing.T, plan Plan, assessments []Assessment) {
			assessments[1] = newWindowAssessment(t, set, 1, []fillerstructure.Segment{
				{StartMS: 105_000, EndMS: 123_000, Role: fillerstructure.RoleCommercial},
				{StartMS: 123_000, EndMS: 255_000, Role: fillerstructure.RolePromo},
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assessments := assessmentsForTimeline(t, set, truth)
			test.mutate(t, plan, assessments)
			result, err := Stitch(set, assessments, 2_000)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StitchHeld || result.HoldReason != HoldOverlapConflict || len(result.Segments) != 0 {
				t.Fatalf("conflict stitch = %+v", result)
			}
		})
	}
}

func TestStitchRetainsOperationalFailureAsWholeFamilyHold(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	truth := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	assessments := assessmentsForTimeline(t, set, truth)
	input := assessmentInputFixture(set, 1, nil)
	input.Failure = "provider_timeout"
	assessments[1], err = NewAssessment(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Stitch(set, assessments, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StitchHeld || result.HoldReason != HoldOperationalFailure || len(result.Segments) != 0 {
		t.Fatalf("operational hold = %+v", result)
	}
}

func TestStitchRejectsMissingOrMixedAssessmentAuthority(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	truth := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	assessments := assessmentsForTimeline(t, set, truth)
	if _, err := Stitch(set, assessments[:2], 2_000); err == nil {
		t.Fatal("missing window assessment was accepted")
	}
	input := assessmentInputFixture(set, 1, clipTimeline(truth, plan.Windows[1]))
	input.Assessor.ID = "assessor-b"
	input.Assessor.ModelFamily = "family-b"
	assessments[1], err = NewAssessment(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Stitch(set, assessments, 2_000); err == nil {
		t.Fatal("mixed assessor profiles were accepted")
	}
}

func TestValidateStitchResultRejectsRehashedProjectionDrift(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	truth := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	result, err := Stitch(set, assessmentsForTimeline(t, set, truth), 2_000)
	if err != nil {
		t.Fatal(err)
	}
	result.Segments[0].Role = fillerstructure.RolePromo
	result.SHA256 = StitchSHA256(result)
	if err := ValidateStitchResult(result); err == nil {
		t.Fatal("rehashed projection drift was accepted")
	}
}

func assessmentsForTimeline(t *testing.T, set MediaSet, timeline []fillerstructure.Segment) []Assessment {
	t.Helper()
	plan := set.Plan
	assessments := make([]Assessment, len(plan.Windows))
	for ordinal, window := range plan.Windows {
		assessments[ordinal] = newWindowAssessment(t, set, ordinal, clipTimeline(timeline, window))
	}
	return assessments
}

func newWindowAssessment(t *testing.T, set MediaSet, ordinal int, segments []fillerstructure.Segment) Assessment {
	t.Helper()
	input := assessmentInputFixture(set, ordinal, segments)
	assessment, err := NewAssessment(input)
	if err != nil {
		t.Fatal(err)
	}
	return assessment
}

func clipTimeline(timeline []fillerstructure.Segment, window Window) []fillerstructure.Segment {
	var clipped []fillerstructure.Segment
	for _, segment := range timeline {
		start := max(segment.StartMS, window.MediaStartMS)
		end := min(segment.EndMS, window.MediaEndMS)
		if start < end {
			clipped = append(clipped, fillerstructure.Segment{StartMS: start, EndMS: end, Role: segment.Role})
		}
	}
	return clipped
}
