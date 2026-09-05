package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRequiresCompleteAnchorAdjudicationAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, required := range []string{"challenge authority", "plan authoring", "receipt", "--assessment", "comparison", "complete-playback submission", "case count", "adjudication time", "output"} {
		if !strings.Contains(stderr.String(), required) {
			t.Fatalf("usage error omits %q: %s", required, stderr.String())
		}
	}
}

func TestAssessmentPathsRejectsEmptyAndPreservesOrder(t *testing.T) {
	var paths assessmentPaths
	if err := paths.Set(""); err == nil {
		t.Fatal("empty assessment path was accepted")
	}
	if err := paths.Set("second.json"); err != nil {
		t.Fatal(err)
	}
	if err := paths.Set("first.json"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "second.json" || paths[1] != "first.json" {
		t.Fatalf("paths = %v", paths)
	}
}
