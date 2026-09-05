package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresCompleteBoundInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "prior source root/count") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPublishIsImmutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inspection.json")
	if err := publish(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := publish(path, []byte("second")); err == nil {
		t.Fatal("existing inspection overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first" {
		t.Fatalf("published data=%q err=%v", data, err)
	}
}
