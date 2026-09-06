package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRequestRejectsStrictJSONViolations(t *testing.T) {
	for name, raw := range map[string][]byte{"valid": []byte(`{"alias":"x"}`), "duplicate": []byte(`{"alias":"a","alias":"b"}`), "case alias": []byte(`{"alias":"a","Alias":"b"}`), "invalid utf8": {'{', '"', 'a', 'l', 'i', 'a', 's', '"', ':', '"', 0xff, '"', '}'}, "trailing": []byte(`{} {}`), "unknown": []byte(`{"unexpected":true}`)} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(p, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readRequest(p)
			if (name == "valid") != (err == nil) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
