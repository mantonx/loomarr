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
		"suitability", "reference audit", "reference download ledger", "families", "transitions", "programmes", "source root", "private seed", "fixed planning time", "output",
	} {
		if !strings.Contains(stderr.String(), required) {
			t.Fatalf("usage error omits %q: %s", required, stderr.String())
		}
	}
}
