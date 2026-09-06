package fillerstructurewindow

import (
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestMediaSetBindsEveryPlannedWindow(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set, err := NewMediaSet(plan, mediaSetFixture(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Windows) != len(plan.Windows) || set.Plan.SHA256 != plan.SHA256 ||
		set.SHA256 == "" || set.SHA256 != MediaSetSHA256(set) {
		t.Fatalf("media set = %+v", set)
	}
	if err := ValidateMediaSet(set); err != nil {
		t.Fatal(err)
	}
}

func TestMediaSetRejectsMissingAndDriftedWindowAuthority(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	valid, err := NewMediaSet(plan, mediaSetFixture(plan))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*MediaSet)
	}{
		{name: "missing", mutate: func(set *MediaSet) { set.Windows = set.Windows[:len(set.Windows)-1] }},
		{name: "ordinal", mutate: func(set *MediaSet) { set.Windows[1].Ordinal++ }},
		{name: "media", mutate: func(set *MediaSet) { set.Windows[1].Media.SHA256 = strings.Repeat("F", 64) }},
		{name: "lineage", mutate: func(set *MediaSet) { set.Windows[1].Media.LineageSHA256 = "" }},
		{name: "profile", mutate: func(set *MediaSet) { set.Windows[1].Media.ProfileSHA256 = strings.Repeat("e", 64) }},
		{name: "duration", mutate: func(set *MediaSet) { set.Windows[1].Media.DurationMS += 1_001 }},
		{name: "bytes", mutate: func(set *MediaSet) { set.Windows[1].Media.Bytes = set.Plan.Profile.MaximumWindowBytes + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set := valid
			set.Windows = append([]WindowMedia(nil), valid.Windows...)
			test.mutate(&set)
			set.SHA256 = MediaSetSHA256(set)
			if err := ValidateMediaSet(set); err == nil {
				t.Fatal("drifted media set was accepted")
			}
		})
	}
}

func mediaSetFixture(plan Plan) []fillerstructure.AssessmentMedia {
	media := make([]fillerstructure.AssessmentMedia, len(plan.Windows))
	for ordinal, window := range plan.Windows {
		media[ordinal] = fillerstructure.AssessmentMedia{
			SHA256: strings.Repeat(string(rune('a'+ordinal)), 64), Bytes: 1_024,
			DurationMS:    window.MediaEndMS - window.MediaStartMS,
			ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256,
			LineageSHA256: strings.Repeat(string(rune('d'+ordinal)), 64),
		}
	}
	return media
}
