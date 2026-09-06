package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresCompleteKnownScriptPreparationInputs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunDoesNotPrintPrivateInputPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	privateAuthority := filepath.Join(root, "participant-secret-authority.json")
	args := []string{
		"--authority", privateAuthority,
		"--source-root", filepath.Join(root, "source"),
		"--seed", filepath.Join(root, "secret-seed.bin"),
		"--ffmpeg", "fixture-ffmpeg",
		"--ffprobe", "fixture-ffprobe",
		"--prepared-at", "2026-09-04T12:00:00Z",
		"--expected-speakers", "59",
		"--max-input-bytes", "1048576",
		"--max-output-bytes", "1048576",
		"--max-wall-time", "1m",
		"--output", filepath.Join(root, "prepared"),
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 1 || stdout.Len() != 0 ||
		strings.Contains(stderr.String(), root) || strings.Contains(stderr.String(), "participant-secret") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
