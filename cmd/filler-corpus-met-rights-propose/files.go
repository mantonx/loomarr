package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func readBoundedRegularFile(filename string, maximumBytes int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximumBytes {
		return nil, fmt.Errorf("input must be a non-empty regular file of at most %d bytes", maximumBytes)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
		return nil, fmt.Errorf("input changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != info.Size() || int64(len(raw)) > maximumBytes {
		return nil, fmt.Errorf("input size changed while reading")
	}
	return raw, nil
}

func writePrivateJSONExclusive(filename string, value any) error {
	if !filepath.IsAbs(filename) {
		return fmt.Errorf("output path must be absolute")
	}
	parent := filepath.Dir(filepath.Clean(filename))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("output parent must be a private regular directory")
	}
	temp, err := os.CreateTemp(parent, ".met-rights-prescreen-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	published := false
	defer func() {
		_ = temp.Close()
		if !published {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, filename); err != nil {
		return err
	}
	published = true
	if err := os.Remove(tempName); err != nil {
		return err
	}
	return nil
}
