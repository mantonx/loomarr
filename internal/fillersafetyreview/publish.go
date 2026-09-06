package fillersafetyreview

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

func rejectPrematureOutput(path string, state checkpoint) error {
	if !state.CompletedAt.IsZero() {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("model review output already exists before review completion")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect model review output before execution: %w", err)
	}
	return nil
}

func publishPrivate(path string, raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("model review output is empty")
	}
	if existing, err := readPrivateFile(path, maximumDocumentBytes); err == nil {
		if bytes.Equal(existing, raw) {
			return nil
		}
		return fmt.Errorf("model review output already exists with different bytes")
	} else if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return fmt.Errorf("model review output path is not a new private file")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create model review output parent: %w", err)
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("model review output parent must be private and not a symlink")
	}
	temporary, err := os.CreateTemp(parent, ".model-review-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if existing, readErr := readPrivateFile(path, maximumDocumentBytes); readErr == nil && bytes.Equal(existing, raw) {
			return nil
		}
		return fmt.Errorf("publish model review output without overwrite: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	return syncDirectory(parent)
}
