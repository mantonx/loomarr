package fillervisualsafety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const maximumPortableWorkerDiagnosticBytes = 64 << 10

func resolvePortableWorker(ctx context.Context, configured string, expected ToolIdentity) (string, error) {
	if !validTool(expected) || strings.TrimSpace(configured) == "" {
		return "", errors.New("portable visual-safety worker identity is invalid")
	}
	path, err := exec.LookPath(configured)
	if err != nil {
		return "", fmt.Errorf("portable visual-safety worker lookup: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("portable visual-safety worker symlink: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil || filepath.Clean(path) != path {
		return "", errors.New("portable visual-safety worker path is invalid")
	}
	if err := verifyPortableWorker(ctx, path, expected); err != nil {
		return "", err
	}
	return path, nil
}

func verifyPortableWorker(ctx context.Context, path string, expected ToolIdentity) error {
	if ctx == nil || ctx.Err() != nil || !validTool(expected) {
		return errors.New("portable visual-safety worker identity is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > MaximumSourceBytes {
		return errors.New("portable visual-safety worker is not a bounded regular file")
	}
	file, err := os.Open(path) //nolint:gosec // resolved local executable whose complete bytes are verified
	if err != nil {
		return errors.New("portable visual-safety worker could not be opened")
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	buffer := make([]byte, 128<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			total += int64(count)
			if total > info.Size() {
				return errors.New("portable visual-safety worker bytes changed")
			}
			_, _ = digest.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return errors.New("portable visual-safety worker could not be read")
		}
	}
	if total != info.Size() || hex.EncodeToString(digest.Sum(nil)) != expected.ExecutableSHA256 {
		return errors.New("portable visual-safety worker executable identity drifted")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || after.Size() != info.Size() || after.ModTime() != info.ModTime() {
		return errors.New("portable visual-safety worker bytes changed")
	}
	return nil
}

type portableWorkerDiagnostics struct {
	mu       sync.Mutex
	builder  strings.Builder
	overflow bool
}

func (diagnostics *portableWorkerDiagnostics) Write(payload []byte) (int, error) {
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	remaining := maximumPortableWorkerDiagnosticBytes - diagnostics.builder.Len()
	if remaining > 0 {
		_, _ = diagnostics.builder.Write(payload[:min(len(payload), remaining)])
	}
	if len(payload) > remaining {
		diagnostics.overflow = true
	}
	return len(payload), nil
}

func (diagnostics *portableWorkerDiagnostics) String() string {
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	return strings.TrimSpace(diagnostics.builder.String())
}

func (diagnostics *portableWorkerDiagnostics) Overflowed() bool {
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	return diagnostics.overflow
}

func portableWorkerFailure(processErr error, diagnostics string) error {
	if diagnostics == "" {
		return fmt.Errorf("portable visual-safety worker failed: %w", processErr)
	}
	lines := strings.Split(diagnostics, "\n")
	return fmt.Errorf("portable visual-safety worker failed: %w: %s", processErr, lines[len(lines)-1])
}
