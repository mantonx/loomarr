package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRequestRejectsStrictJSONViolations(t *testing.T) {
	valid := []byte(`{"authority":{},"sourceRoot":"/source","policyPath":"/policy","aliasSeedPath":"/alias","preparedAt":"2026-09-06T00:00:00Z"}`)
	for name, raw := range map[string][]byte{
		"valid":        valid,
		"duplicate":    []byte(`{"sourceRoot":"/a","sourceRoot":"/b"}`),
		"case alias":   []byte(`{"sourceRoot":"/a","SourceRoot":"/b"}`),
		"invalid utf8": {'{', '"', 's', 'o', 'u', 'r', 'c', 'e', 'R', 'o', 'o', 't', '"', ':', '"', 0xff, '"', '}'},
		"trailing":     []byte(`{} {}`),
		"unknown":      []byte(`{"unexpected":true}`),
	} {
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
