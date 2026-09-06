package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadTermSetRequiresOneStrictSchemaValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terms.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"terms":["adam and eve","venus"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	terms, err := readTermSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(terms.Terms, []string{"adam and eve", "venus"}) {
		t.Fatalf("terms = %#v", terms.Terms)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"terms":["venus"],"modelAnswer":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTermSet(path); err == nil {
		t.Fatal("unknown field passed")
	}
}

func TestRunRejectsUnboundedCaptureBeforeNetwork(t *testing.T) {
	var stdout, stderr strings.Builder
	code := run([]string{"--snapshot-at", "2026-09-04T12:00:00Z"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "explicit request/item/byte/time ceilings") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestWriteJSONUsesPrivateFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "inventory.json")
	if err := writeJSON(path, termSet{SchemaVersion: 1, Terms: []string{"venus"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if parent.Mode().Perm() != 0o700 {
		t.Fatalf("parent mode = %o", parent.Mode().Perm())
	}
}
