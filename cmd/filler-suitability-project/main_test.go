package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRequiresCompleteProjectionAuthority(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "comparison, two results") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
