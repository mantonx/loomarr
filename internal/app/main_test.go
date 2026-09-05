package app

import (
	"fmt"
	"os"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit"
)

// TestMain makes the documented focused-package command self-contained. Application construction
// performs the production worker handshake, so it must receive the real release-matched test worker
// even when this package is the first thing run in a fresh worktree.
func TestMain(m *testing.M) {
	worker, err := testkit.RustImageWorker()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare Rust image worker: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("LOOMARR_IMAGE_WORKER", worker); err != nil {
		fmt.Fprintf(os.Stderr, "select Rust image worker: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
