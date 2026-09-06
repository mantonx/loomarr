package fillersafetyreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

func loadCheckpoint(dir string, identity checkpointIdentity, now time.Time) (checkpoint, error) {
	if err := ensureCheckpointDirectory(dir); err != nil {
		return checkpoint{}, err
	}
	path := filepath.Join(dir, checkpointFilename)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		value := checkpoint{Identity: identity, StartedAt: now.UTC()}
		if err := persistCheckpoint(dir, value); err != nil {
			return checkpoint{}, err
		}
		return value, nil
	}
	value, _, err := readPrivateJSON[checkpoint](path, maximumDocumentBytes)
	if err != nil {
		return checkpoint{}, fmt.Errorf("read model review checkpoint: %w", err)
	}
	if !reflect.DeepEqual(value.Identity, identity) {
		return checkpoint{}, fmt.Errorf("model review checkpoint identity drift")
	}
	if err := validateCheckpoint(value); err != nil {
		return checkpoint{}, err
	}
	return value, nil
}

func persistCheckpoint(dir string, value checkpoint) error {
	if err := validateCheckpoint(value); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(dir, ".checkpoint-*")
	if err != nil {
		return fmt.Errorf("create model review checkpoint: %w", err)
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
	if err := os.Rename(temporaryPath, filepath.Join(dir, checkpointFilename)); err != nil {
		return fmt.Errorf("publish model review checkpoint: %w", err)
	}
	return syncDirectory(dir)
}

func ensureCheckpointDirectory(dir string) error {
	abs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return fmt.Errorf("create model review checkpoint parent: %w", err)
		}
		if err := os.Mkdir(abs, 0o700); err != nil {
			return fmt.Errorf("create model review checkpoint directory: %w", err)
		}
		return syncDirectory(filepath.Dir(abs))
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("model review checkpoint directory must be private and not a symlink")
	}
	return nil
}

type activeLock struct {
	path string
}

func acquireActiveLock(dir string) (*activeLock, error) {
	path := filepath.Join(dir, activeLockFilename)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("model review checkpoint is already active or locked")
	}
	if _, err := file.WriteString("active\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := syncDirectory(dir); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &activeLock{path: path}, nil
}

func (lock *activeLock) release() error {
	if lock == nil || lock.path == "" {
		return nil
	}
	if err := os.Remove(lock.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(lock.path)
	lock.path = ""
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
