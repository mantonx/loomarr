//go:build !windows

package execfixture

import "testing"

// POSIX writes one sh-backed executable test double.
func POSIX(t testing.TB, name, body string) string {
	t.Helper()
	return Executable(t, name, "#!/bin/sh\n"+body+"\n")
}
