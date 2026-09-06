package fillerstructurewindow

import (
	"reflect"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestParseDirectVideoResponseProjectsWindowLocalTimelineOntoSource(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	window := set.Plan.Windows[1]
	duration := window.MediaEndMS - window.MediaStartMS
	raw := `{"segments":[{"endMs":15000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"},{"endMs":` + integerString(duration) + `,"role":"promo","decisiveAtMs":[20000],"reason":"promotion"}]}`
	segments, err := ParseDirectVideoResponse(set, 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []fillerstructure.Segment{
		{StartMS: window.MediaStartMS, EndMS: window.MediaStartMS + 15_000, Role: fillerstructure.RoleCommercial},
		{StartMS: window.MediaStartMS + 15_000, EndMS: window.MediaEndMS, Role: fillerstructure.RolePromo},
	}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("segments=%+v, want %+v", segments, want)
	}
}

func TestParseDirectVideoResponseRejectsIncompleteWindowTimeline(t *testing.T) {
	plan, err := NewPlan(sourceFixture(300_000))
	if err != nil {
		t.Fatal(err)
	}
	set := mediaSetForPlan(t, plan)
	if _, err := ParseDirectVideoResponse(set, 1, `{"segments":[{"endMs":1000,"role":"commercial","decisiveAtMs":[500],"reason":"offer"}]}`); err == nil {
		t.Fatal("incomplete window timeline was accepted")
	}
}

func integerString(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
