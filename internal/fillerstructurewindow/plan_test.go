package fillerstructurewindow

import (
	"reflect"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestNewPlanPartitionsPrimaryCoverageAndExpandsContext(t *testing.T) {
	tests := []struct {
		name     string
		duration int64
		want     []Window
	}{
		{name: "short", duration: 60_000, want: []Window{{Ordinal: 0, PrimaryStartMS: 0, PrimaryEndMS: 60_000, MediaStartMS: 0, MediaEndMS: 60_000}}},
		{name: "exact primary", duration: 120_000, want: []Window{{Ordinal: 0, PrimaryStartMS: 0, PrimaryEndMS: 120_000, MediaStartMS: 0, MediaEndMS: 120_000}}},
		{name: "one millisecond over", duration: 120_001, want: []Window{
			{Ordinal: 0, PrimaryStartMS: 0, PrimaryEndMS: 120_000, MediaStartMS: 0, MediaEndMS: 120_001, RightContextMS: 1},
			{Ordinal: 1, PrimaryStartMS: 120_000, PrimaryEndMS: 120_001, MediaStartMS: 105_000, MediaEndMS: 120_001, LeftContextMS: 15_000},
		}},
		{name: "three windows", duration: 300_000, want: []Window{
			{Ordinal: 0, PrimaryStartMS: 0, PrimaryEndMS: 120_000, MediaStartMS: 0, MediaEndMS: 135_000, RightContextMS: 15_000},
			{Ordinal: 1, PrimaryStartMS: 120_000, PrimaryEndMS: 240_000, MediaStartMS: 105_000, MediaEndMS: 255_000, LeftContextMS: 15_000, RightContextMS: 15_000},
			{Ordinal: 2, PrimaryStartMS: 240_000, PrimaryEndMS: 300_000, MediaStartMS: 225_000, MediaEndMS: 300_000, LeftContextMS: 15_000},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := NewPlan(sourceFixture(test.duration))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(plan.Windows, test.want) || plan.SHA256 == "" || plan.SHA256 != PlanSHA256(plan) {
				t.Fatalf("plan = %+v\nwant = %+v", plan, test.want)
			}
		})
	}
}

func TestPlanBoundaryOwnershipUsesRightHandWindowAtSeam(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		at      int64
		ordinal int
	}{
		{at: 1, ordinal: 0},
		{at: 119_999, ordinal: 0},
		{at: 120_000, ordinal: 1},
		{at: 239_999, ordinal: 1},
		{at: 240_000, ordinal: 2},
		{at: 299_999, ordinal: 2},
	}
	for _, test := range tests {
		owner, err := plan.BoundaryOwner(test.at)
		if err != nil || owner.Ordinal != test.ordinal {
			t.Fatalf("boundary %d owner=%+v error=%v, want ordinal %d", test.at, owner, err, test.ordinal)
		}
	}
	for _, edge := range []int64{-1, 0, 300_000, 300_001} {
		if _, err := plan.BoundaryOwner(edge); err == nil {
			t.Fatalf("source edge %d received a boundary owner", edge)
		}
	}
}

func TestPlanSupportsExactlyBoundedProtocolCapacity(t *testing.T) {
	plan, err := NewPlan(sourceFixture(MaximumSourceDurationMS))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Windows) != MaximumWindows || plan.Windows[len(plan.Windows)-1].PrimaryEndMS != MaximumSourceDurationMS {
		t.Fatalf("maximum plan = %+v", plan)
	}
	if _, err := NewPlan(sourceFixture(MaximumSourceDurationMS + 1)); err == nil {
		t.Fatal("source above protocol capacity was accepted")
	}
}

func TestValidatePlanRejectsIdentityAndCoverageDrift(t *testing.T) {
	base, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{name: "profile", mutate: func(plan *Plan) { plan.Profile.ContextOverlapMS++ }},
		{name: "source", mutate: func(plan *Plan) { plan.Source.SHA256 = "" }},
		{name: "gap", mutate: func(plan *Plan) { plan.Windows[1].PrimaryStartMS++ }},
		{name: "overlap", mutate: func(plan *Plan) { plan.Windows[1].PrimaryStartMS-- }},
		{name: "context", mutate: func(plan *Plan) { plan.Windows[1].MediaStartMS-- }},
		{name: "ordinal", mutate: func(plan *Plan) { plan.Windows[1].Ordinal++ }},
		{name: "digest", mutate: func(plan *Plan) { plan.SHA256 = strings.Repeat("f", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			plan.Windows = append([]Window(nil), base.Windows...)
			test.mutate(&plan)
			if err := ValidatePlan(plan); err == nil {
				t.Fatal("drifted plan was accepted")
			}
		})
	}
}

func TestCanonicalProfileIsStableAndBindsAssessmentMedia(t *testing.T) {
	profile := CanonicalProfile()
	if profile.SHA256 != "edc18ef1b1605ba3ad5460bbd0b5a415048c4099db9acbc257b21875daa2783c" ||
		profile.SHA256 != ProfileSHA256(profile) ||
		profile.MaximumWindowDurationMS != 150_000 || profile.MaximumWindows != 15 ||
		profile.MaximumWindowBytes != 64<<20 || profile.MaximumTimelineDriftMS != 1_000 ||
		profile.AssessmentMediaProfileSHA256 == "" {
		t.Fatalf("profile = %+v", profile)
	}
	second := CanonicalProfile()
	if !reflect.DeepEqual(profile, second) {
		t.Fatalf("canonical profile drifted: first=%+v second=%+v", profile, second)
	}
}

func sourceFixture(durationMS int64) fillerstructure.Source {
	return fillerstructure.Source{SHA256: strings.Repeat("a", 64), Bytes: 1_024, DurationMS: durationMS}
}
