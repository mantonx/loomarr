package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type strictNominationDocument struct {
	Name  string   `json:"name"`
	Items []string `json:"items"`
}

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

func TestDecodeStrictJSONRejectsAmbiguousDocuments(t *testing.T) {
	t.Parallel()
	invalidUTF8 := append([]byte(`{"name":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`","items":[]}`)...)
	tests := map[string][]byte{
		"unknown field":  []byte(`{"name":"alpha","items":[],"extra":true}`),
		"case variant":   []byte(`{"Name":"alpha","items":[]}`),
		"duplicate name": []byte(`{"name":"first","name":"second","items":[]}`),
		"invalid UTF-8":  invalidUTF8,
		"trailing value": []byte(`{"name":"alpha","items":[]} {}`),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if err := decodeStrictJSON(data, new(strictNominationDocument)); err == nil {
				t.Fatal("decodeStrictJSON accepted an ambiguous document")
			}
		})
	}
}

func TestDecodeStrictJSONPreservesNilAndEmptyCollections(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		data    string
		wantNil bool
	}{
		{name: "null", data: `{"name":"alpha","items":null}`, wantNil: true},
		{name: "empty", data: `{"name":"alpha","items":[]}`, wantNil: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got strictNominationDocument
			if err := decodeStrictJSON([]byte(test.data), &got); err != nil {
				t.Fatal(err)
			}
			if (got.Items == nil) != test.wantNil || len(got.Items) != 0 {
				t.Fatalf("items = %#v, want nil=%t and length 0", got.Items, test.wantNil)
			}
		})
	}
}
