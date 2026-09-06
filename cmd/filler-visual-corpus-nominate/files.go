package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func readPrivateInput(path string) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("path must be absolute and clean")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 || info.Size() > maximumNominationInputBytes {
		return nil, errors.New("input is not a bounded private regular file")
	}
	file, err := os.Open(path) //nolint:gosec // exact private path validated above
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximumNominationInputBytes+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, errors.New("input bytes drifted")
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("input identity drifted")
	}
	return raw, nil
}

func decodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func publishWorksheetDirectory(output string, worksheet, reviewCSV, board []byte) error {
	if output == "" || !filepath.IsAbs(output) || filepath.Clean(output) != output {
		return errors.New("worksheet output must be an absolute clean path")
	}
	parent := filepath.Dir(output)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("worksheet parent is invalid")
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		return errors.New("worksheet output already exists or cannot be created")
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(output)
		}
	}()
	for _, file := range []struct {
		name string
		raw  []byte
	}{{".incomplete", []byte("incomplete\n")}, {nominationWorksheetFilename, worksheet},
		{nominationReviewFilename, reviewCSV}, {nominationBoardFilename, board}} {
		if err := writePrivateFile(filepath.Join(output, file.name), file.raw); err != nil {
			return err
		}
	}
	if err := syncDirectory(output); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(output, ".incomplete")); err != nil {
		return err
	}
	if err := syncDirectory(output); err != nil {
		return err
	}
	if err := syncDirectory(parent); err != nil {
		return err
	}
	published = true
	return nil
}

func writePrivateFile(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, err := file.Write(raw); err != nil {
		writeErr = err
	} else if err := file.Sync(); err != nil {
		writeErr = err
	}
	if err := file.Close(); writeErr == nil {
		writeErr = err
	}
	return writeErr
}

func syncDirectory(path string) error {
	directory, err := os.Open(path) //nolint:gosec // exact directory path validated by caller
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
