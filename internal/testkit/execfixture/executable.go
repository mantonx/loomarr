// Package execfixture owns filesystem-backed executable test doubles without
// importing application packages. It is the cycle-free leaf beneath testkit.
package execfixture

import (
	"os"
	"path/filepath"
	"testing"
)

// Executable writes one executable test double.
func Executable(t testing.TB, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write executable %s: %v", name, err)
	}
	return path
}
