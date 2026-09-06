package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRequiresKnownSubcommand(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"unknown"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || stderr.Len() == 0 {
			t.Fatalf("run(%v) = %d, stderr %q", args, code, stderr.String())
		}
	}
}

func TestPrivateNominationInputsAndWorksheetPublication(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	privatePath := filepath.Join(root, "private.json")
	if err := os.WriteFile(privatePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if raw, err := readPrivateInput(privatePath); err != nil || string(raw) != "{}\n" {
		t.Fatalf("readPrivateInput() = %q, %v", raw, err)
	}
	if err := os.Chmod(privatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateInput(privatePath); err == nil {
		t.Fatal("readPrivateInput accepted a non-private file")
	}

	output := filepath.Join(root, "worksheet")
	if err := publishWorksheetDirectory(output, []byte("worksheet\n"), []byte("review\n"), []byte("board\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(output)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("worksheet directory = %v, %v", info, err)
	}
	for _, name := range []string{nominationWorksheetFilename, nominationReviewFilename, nominationBoardFilename} {
		info, err := os.Lstat(filepath.Join(output, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("worksheet file %s = %v, %v", name, info, err)
		}
	}
	if err := publishWorksheetDirectory(output, []byte("changed"), []byte("changed"), []byte("changed")); err == nil {
		t.Fatal("publishWorksheetDirectory overwrote an existing review")
	}
}
