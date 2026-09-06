//go:build !windows

package testkit

import (
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/execfixture"
)

// POSIXExecutable writes a shared sh-backed system-boundary double. Callers supply only the body so
// packages do not grow subtly different private executable factories.
func POSIXExecutable(t *testing.T, name, body string) string {
	t.Helper()
	return execfixture.POSIX(t, name, body)
}
