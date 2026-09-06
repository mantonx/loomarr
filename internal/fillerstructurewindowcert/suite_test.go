package fillerstructurewindowcert

import (
	"slices"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestNewSuiteRejectsCasesWithoutTwoWindows(t *testing.T) {
	suite, _ := certificationFixture(t)
	for _, test := range []struct {
		name  string
		nil   bool
		count int
	}{
		{name: "nil", nil: true},
		{name: "zero"},
		{name: "one", count: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			cases := slices.Clone(suite.Cases)
			if test.nil {
				cases[0].MediaSet.Plan.Windows = nil
				cases[0].MediaSet.Windows = nil
			} else {
				cases[0].MediaSet.Plan.Windows = slices.Clone(cases[0].MediaSet.Plan.Windows[:test.count])
				cases[0].MediaSet.Windows = slices.Clone(cases[0].MediaSet.Windows[:test.count])
			}
			if _, err := NewSuite(cases); err == nil {
				t.Fatal("NewSuite accepted a case without two planned windows")
			}
		})
	}
}

func TestNewSuiteHighByteCohortRanksWindowsAndRequiresCaseCoverage(t *testing.T) {
	suite, _ := certificationFixture(t)
	concentrated := slices.Clone(suite.Cases)
	concentrated[0].MediaSet = mediaSetWithBytes(t, concentrated[0].MediaSet, []int64{60 << 20, 59 << 20, 58 << 20})
	concentrated[1].MediaSet = mediaSetWithBytes(t, concentrated[1].MediaSet, []int64{57 << 20, 56 << 20, 55 << 20})
	if _, err := NewSuite(concentrated); err == nil {
		t.Fatal("NewSuite accepted a high-byte cohort concentrated in fewer than six cases")
	}

	valid, err := NewSuite(suite.Cases)
	if err != nil {
		t.Fatal(err)
	}
	if valid.HighByteMinimumBytes != 30<<20 {
		t.Fatalf("high-byte minimum = %d, want %d", valid.HighByteMinimumBytes, int64(30<<20))
	}
	for _, item := range valid.Cases {
		if !slices.Contains(item.Slices, SliceHighByteWindow) {
			t.Fatalf("case %q missing high-byte slice", item.ID)
		}
	}
}

func mediaSetWithBytes(t *testing.T, set fillerstructurewindow.MediaSet, bytes []int64) fillerstructurewindow.MediaSet {
	t.Helper()
	media := make([]fillerstructure.AssessmentMedia, len(set.Windows))
	for ordinal, window := range set.Windows {
		media[ordinal] = window.Media
		if ordinal < len(bytes) {
			media[ordinal].Bytes = bytes[ordinal]
		}
	}
	rebuilt, err := fillerstructurewindow.NewMediaSet(set.Plan, media)
	if err != nil {
		t.Fatal(err)
	}
	return rebuilt
}
