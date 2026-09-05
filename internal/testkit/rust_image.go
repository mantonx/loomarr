package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/loomarr/loomarr/internal/images/rustgen"
)

var rustImageWorkerBuild struct {
	sync.Once
	path string
	err  error
}

// RustImageWorker returns the same required worker used in production. A focused `go test` is a
// supported repository command and may be the first command run in a fresh worktree, so the testkit
// owns its incremental, locked debug build instead of requiring an undocumented Cargo preflight.
// The comprehensive gate still builds the worker explicitly so its build is visible as a gate step.
func RustImageWorker() (string, error) {
	if override := os.Getenv("LOOMARR_IMAGE_WORKER"); override != "" {
		return override, nil
	}
	rustImageWorkerBuild.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			rustImageWorkerBuild.err = fmt.Errorf("locate testkit source")
			return
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
		rustImageWorkerBuild.path, rustImageWorkerBuild.err = buildRustImageWorker(root, runCargoBuild)
	})
	return rustImageWorkerBuild.path, rustImageWorkerBuild.err
}

func buildRustImageWorker(root string, build func(string) error) (string, error) {
	name := "loomarr-image"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	worker := filepath.Join(root, "target", "debug", name)
	if info, err := os.Stat(worker); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("built Rust image worker is not a regular file: %s", worker)
		}
		return worker, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("locate Rust image worker: %w", err)
	}
	if err := build(root); err != nil {
		return "", err
	}
	info, err := os.Stat(worker)
	if err != nil {
		return "", fmt.Errorf("locate built Rust image worker: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("built Rust image worker is not a regular file: %s", worker)
	}
	return worker, nil
}

func runCargoBuild(root string) error {
	cargo := os.Getenv("CARGO")
	if cargo == "" {
		cargo = "cargo"
	}
	cmd := exec.Command(cargo, "build", "--locked", "-p", "loomarr-image")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LOOMARR_RELEASE=dev")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build required Rust image worker: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// RustImageRenderer opens the real test worker. Using the executable here keeps protocol and pixel
// behavior from drifting behind an in-process fake.
func RustImageRenderer(t testing.TB) *rustgen.Generator {
	t.Helper()
	worker, err := RustImageWorker()
	if err != nil {
		t.Fatal(err)
	}
	gen, err := rustgen.Open(worker, rustgen.Contract{
		Protocol: 1, Release: "dev", Recipe: "loomarr-rendition-v2",
		RequiredFormats: []string{"avif", "jpeg", "webp"}, Animation: true,
	})
	if err != nil {
		t.Fatalf("open required Rust image worker: %v", err)
	}
	return gen
}
