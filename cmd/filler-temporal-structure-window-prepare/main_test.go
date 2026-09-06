package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRequiresCompleteWindowPreparationAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "corpus manifest, private authority") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
