package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func publish(path string, data []byte) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".filler-corpus-quarantine-inspect-*")
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
	if err := temp.Chmod(0o640); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, absolute); err != nil {
		return fmt.Errorf("publish immutable quarantine inspection: %w", err)
	}
	if err := os.Remove(tempName); err != nil {
		_ = os.Remove(absolute)
		return err
	}
	published = true
	return nil
}
