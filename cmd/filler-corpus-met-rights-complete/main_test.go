package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresOneExplicitTransition(t *testing.T) {
	tests := [][]string{
		nil,
		{"--mode", "unknown", "--inventory", "i", "--worksheet", "w", "--prescreen", "p"},
		{"--mode", "prepare", "--inventory", "i", "--worksheet", "w", "--prescreen", "p", "--attestation", "a", "--attestation-out", "o"},
		{"--mode", "complete", "--inventory", "i", "--worksheet", "w", "--prescreen", "p", "--completed-csv-out", "o"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("run(%q) = %d, stderr = %s", args, code, stderr.String())
		}
	}
}

func TestPrivateFileContractRejectsPublicInputAndExistingOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateBoundedRegularFile(input, 1024); err == nil {
		t.Fatal("public input passed")
	}
	if err := os.Chmod(input, 0o600); err != nil {
		t.Fatal(err)
	}
	if raw, err := readPrivateBoundedRegularFile(input, 1024); err != nil || string(raw) != "{}" {
		t.Fatalf("read = %q, %v", raw, err)
	}
	output := filepath.Join(dir, "output.json")
	if err := writePrivateExclusive(output, func(writer io.Writer) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateExclusive(output, func(writer io.Writer) error { return nil }); err == nil {
		t.Fatal("existing output was replaced")
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestRunRejectsNonPrivateArtifactsBeforeParsing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(dir, "inventory.json"), filepath.Join(dir, "worksheet.json"), filepath.Join(dir, "prescreen.json")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(paths[1], 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--mode", "prepare", "--inventory", paths[0], "--worksheet", paths[1], "--prescreen", paths[2], "--attestation-out", filepath.Join(dir, "out.json")}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "private regular file") {
		t.Fatalf("run = %d, stderr = %s", code, stderr.String())
	}
}
