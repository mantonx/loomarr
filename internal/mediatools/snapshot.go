package mediatools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileSnapshot owns one private, immutable copy of a descriptor-validated
// regular file. Callers must close it after every consumer has finished.
type FileSnapshot struct {
	dir       string
	path      string
	sha256    string
	bytes     int64
	closeOnce sync.Once
	closeErr  error
}

// SnapshotRegularFile refuses symlink traversal, caps bytes independently of
// stat metadata, and binds the returned digest to the bytes copied through the
// opened descriptor.
func SnapshotRegularFile(ctx context.Context, sourcePath string) (*FileSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "loomarr-media-snapshot-")
	if err != nil {
		return nil, fmt.Errorf("%w: create private media snapshot: %v", ErrConditioningOutput, err)
	}
	snapshot, err := snapshotConditioningRegularFile(ctx, dir, "source", sourcePath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &FileSnapshot{dir: dir, path: snapshot.path, sha256: snapshot.sha256, bytes: snapshot.bytes}, nil
}

// Path returns the private snapshot path. It retains the source extension so
// media tools can detect formats without consulting the original path.
func (s *FileSnapshot) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// SHA256 returns the lowercase digest of the exact copied bytes.
func (s *FileSnapshot) SHA256() string {
	if s == nil {
		return ""
	}
	return s.sha256
}

// Bytes returns the exact number of copied bytes.
func (s *FileSnapshot) Bytes() int64 {
	if s == nil {
		return 0
	}
	return s.bytes
}

// Close removes the private snapshot and is safe to call repeatedly.
func (s *FileSnapshot) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.dir != "" {
			s.closeErr = os.RemoveAll(filepath.Clean(s.dir))
		}
	})
	return s.closeErr
}
