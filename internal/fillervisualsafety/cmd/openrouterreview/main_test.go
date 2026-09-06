package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPrivateJSONRejectsStrictJSONViolations(t *testing.T) {
	for name, raw := range map[string][]byte{"valid": []byte(`{"model":"x"}`), "duplicate": []byte(`{"model":"a","model":"b"}`), "case alias": []byte(`{"model":"a","Model":"b"}`), "invalid utf8": {'{', '"', 'm', 'o', 'd', 'e', 'l', '"', ':', '"', 0xff, '"', '}'}, "trailing": []byte(`{} {}`), "unknown": []byte(`{"unexpected":true}`)} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(p, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readPrivateJSON[request](p)
			if (name == "valid") != (err == nil) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
