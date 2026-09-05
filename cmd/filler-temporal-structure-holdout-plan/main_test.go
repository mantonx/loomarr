package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRequiresEveryFrozenAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, required := range []string{
		"selection", "evidence", "evidence map", "human assessment", "human attestation", "media quality",
		"suitability", "reference audit", "families", "transitions", "programmes", "source root", "private seed", "lineage mode", "fixed planning time", "output",
	} {
		if !strings.Contains(stderr.String(), required) {
			t.Fatalf("usage error omits %q: %s", required, stderr.String())
		}
	}
}

func TestAdjudicationPathsRejectsEmptyAndPreservesOrder(t *testing.T) {
	var paths adjudicationPaths
	if err := paths.Set(""); err == nil {
		t.Fatal("empty prior adjudication path was accepted")
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
