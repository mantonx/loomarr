package fillerreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPrivateSeedRequiresOneOwnerOnlyLine(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "valid")
	if err := os.WriteFile(valid, []byte("private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed, err := LoadPrivateSeed(valid)
	if err != nil || seed != "private-value" {
		t.Fatalf("seed=%q err=%v", seed, err)
	}
	for name, test := range map[string]struct {
		contents string
		mode     os.FileMode
	}{
		"empty":     {contents: "\n", mode: 0o600},
		"multiline": {contents: "first\nsecond\n", mode: 0o600},
		"readable":  {contents: "private-value\n", mode: 0o644},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte(test.contents), test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPrivateSeed(path); err == nil || !strings.Contains(err.Error(), "private seed") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
