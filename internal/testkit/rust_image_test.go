package testkit

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBuildRustImageWorkerBuildsBeforeResolvingExecutable(t *testing.T) {
	root := t.TempDir()
	built := false
	worker, err := buildRustImageWorker(root, func(gotRoot string) error {
		if gotRoot != root {
			t.Fatalf("build root = %q, want %q", gotRoot, root)
		}
		built = true
		name := "loomarr-image"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(root, "target", "debug", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("worker"), 0o755)
	})
	if err != nil {
		t.Fatalf("buildRustImageWorker: %v", err)
	}
	if !built {
		t.Fatal("Cargo build was not invoked")
	}
	if filepath.Base(worker) != filepath.Base(workerPathForTest()) {
		t.Errorf("worker = %q", worker)
	}
}

func TestBuildRustImageWorkerFailsWhenBuildProducesNoExecutable(t *testing.T) {
	_, err := buildRustImageWorker(t.TempDir(), func(string) error { return nil })
	if err == nil {
		t.Fatal("buildRustImageWorker succeeded without an executable")
	}
}

func TestBuildRustImageWorkerReusesExistingExecutable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target", "debug", workerPathForTest())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	worker, err := buildRustImageWorker(root, func(string) error {
		t.Fatal("build invoked for an existing worker")
		return nil
	})
	if err != nil {
		t.Fatalf("buildRustImageWorker: %v", err)
	}
	if worker != path {
		t.Errorf("worker = %q, want %q", worker, path)
	}
}

func workerPathForTest() string {
	name := "loomarr-image"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}
