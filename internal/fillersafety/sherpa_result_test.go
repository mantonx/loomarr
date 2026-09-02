package fillersafety

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSherpaResultsProjectsAndSortsBoundedIntervals(t *testing.T) {
	t.Parallel()
	rule := "rule-0123456789abcdef01234567"
	variants := map[string][][]string{rule: {{"SAFE", "TOKEN"}}}
	raw := []byte(strings.Join([]string{
		fmt.Sprintf(`{"start_time":0,"keyword":%q,"timestamps":[2,2.12],"tokens":["SAFE","TOKEN"]}`, rule),
		fmt.Sprintf(`{"start_time":0,"keyword":%q,"timestamps":[1,1.08],"tokens":["SAFE","TOKEN"]}`, rule),
		fmt.Sprintf(`{"start_time":0,"keyword":%q,"timestamps":[1,1.08],"tokens":["SAFE","TOKEN"]}`, rule),
	}, "\n"))
	got, err := parseSherpaResults(raw, 3_000, variants)
	if err != nil {
		t.Fatal(err)
	}
	want := []proposedInterval{{StartMS: 1_000, EndMS: 1_120}, {StartMS: 2_000, EndMS: 2_160}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("intervals=%v want=%v", got, want)
	}
}

func TestParseSherpaResultsFailsClosed(t *testing.T) {
	t.Parallel()
	rule := "rule-0123456789abcdef01234567"
	valid := fmt.Sprintf(`{"start_time":0,"keyword":%q,"timestamps":[1],"tokens":["SAFE"]}`, rule)
	variants := map[string][][]string{rule: {{"SAFE"}}}
	tests := []struct{ name, raw string }{
		{name: "blank line", raw: valid + "\n\n"},
		{name: "unknown field", raw: strings.TrimSuffix(valid, "}") + `,"speech":"private"}`},
		{name: "unknown rule", raw: strings.Replace(valid, rule, "rule-1123456789abcdef01234567", 1)},
		{name: "invalid token", raw: strings.Replace(valid, `"SAFE"`, `"\u0000"`, 1)},
		{name: "cardinality", raw: strings.Replace(valid, `[1]`, `[1,1.1]`, 1)},
		{name: "unordered", raw: strings.Replace(valid, `[1]`, `[1.1,1]`, 1)},
		{name: "past source", raw: strings.Replace(valid, `[1]`, `[3]`, 1)},
		{name: "trailing json", raw: valid + `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseSherpaResults([]byte(test.raw), 2_000, variants); err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestParseSherpaResultsAcceptsCompleteNoHitOutput(t *testing.T) {
	t.Parallel()
	got, err := parseSherpaResults(nil, 2_000, map[string][][]string{"rule-0123456789abcdef01234567": {{"SAFE"}}})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("intervals=%v err=%v", got, err)
	}
}
