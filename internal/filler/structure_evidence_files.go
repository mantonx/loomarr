package filler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (r *FileStructureAssessmentEvidenceRepository) putImmutable(ctx context.Context, path string, body []byte, maximum int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(body) == 0 || len(body) > maximum {
		return fmt.Errorf("immutable evidence size is invalid")
	}
	if err := r.ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if existing, err := r.readImmutable(ctx, path, maximum); err == nil {
		if !bytes.Equal(existing, body) {
			return fmt.Errorf("immutable evidence conflicts with existing bytes")
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".structure-evidence-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
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
	if err := os.Link(temporaryName, path); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		existing, readErr := r.readImmutable(ctx, path, maximum)
		if readErr != nil || !bytes.Equal(existing, body) {
			return fmt.Errorf("immutable evidence conflicts with concurrent bytes")
		}
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (r *FileStructureAssessmentEvidenceRepository) readImmutable(ctx context.Context, path string, maximum int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > int64(maximum) {
		return nil, fmt.Errorf("immutable evidence file is unsafe or outside bounds")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || len(raw) == 0 || len(raw) > maximum {
		return nil, fmt.Errorf("read immutable evidence bytes")
	}
	return raw, nil
}

func (r *FileStructureAssessmentEvidenceRepository) ensurePrivateDirectory(directory string) error {
	relative, err := filepath.Rel(r.root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("structure assessment evidence path escapes its root")
	}
	if info, statErr := os.Lstat(r.root); statErr == nil {
		if !privateStructureEvidenceDirectory(info) {
			return fmt.Errorf("structure assessment evidence directory is unsafe")
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	} else if mkdirErr := os.MkdirAll(r.root, 0o700); mkdirErr != nil {
		return mkdirErr
	}
	current := r.root
	parts := []string{}
	if relative != "." {
		parts = strings.Split(relative, string(filepath.Separator))
	}
	for index := -1; index < len(parts); index++ {
		if index >= 0 {
			current = filepath.Join(current, parts[index])
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
				return mkdirErr
			}
		}
		info, statErr := os.Lstat(current)
		if statErr != nil || !privateStructureEvidenceDirectory(info) {
			return fmt.Errorf("structure assessment evidence directory is unsafe")
		}
	}
	return nil
}

func structureEvidenceDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func privateStructureEvidenceDirectory(info fs.FileInfo) bool {
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0o700 == 0o700 && info.Mode().Perm()&0o077 == 0
}
