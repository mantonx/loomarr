package fillersafetycert

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxPrivateDocumentBytes = 64 << 20

type loadedCertification struct {
	authority    Authority
	manifest     ResultManifest
	runs         map[string]ResultRun
	authoritySHA string
	manifestSHA  string
}

func loadCertification(config Config) (loadedCertification, error) {
	if strings.TrimSpace(config.AuthorityPath) == "" || strings.TrimSpace(config.ResultsPath) == "" ||
		strings.TrimSpace(config.OutputPath) == "" || config.ScoredAt.IsZero() {
		return loadedCertification{}, fmt.Errorf("cascade certification requires authority, results, fixed score time, and output")
	}
	paths := []string{config.AuthorityPath, config.ResultsPath, config.OutputPath}
	abs := make([]string, len(paths))
	for index, path := range paths {
		value, err := filepath.Abs(path)
		if err != nil {
			return loadedCertification{}, fmt.Errorf("resolve private document path: %w", err)
		}
		abs[index] = value
	}
	if abs[0] == abs[1] || abs[0] == abs[2] || abs[1] == abs[2] {
		return loadedCertification{}, fmt.Errorf("cascade certification inputs and output must be distinct")
	}

	authority, authorityRaw, err := readPrivateJSON[Authority](config.AuthorityPath)
	if err != nil {
		return loadedCertification{}, fmt.Errorf("read cascade certification authority: %w", err)
	}
	authoritySHA := hashBytes(authorityRaw)
	if err := validateAuthority(authority); err != nil {
		return loadedCertification{}, fmt.Errorf("validate cascade certification authority: %w", err)
	}
	manifest, manifestRaw, err := readPrivateJSON[ResultManifest](config.ResultsPath)
	if err != nil {
		return loadedCertification{}, fmt.Errorf("read cascade result manifest: %w", err)
	}
	if err := validateManifest(authority, authoritySHA, manifest, config.ScoredAt); err != nil {
		return loadedCertification{}, fmt.Errorf("validate cascade result manifest: %w", err)
	}
	runs := make(map[string]ResultRun, len(manifest.Runs))
	for _, run := range manifest.Runs {
		runs[run.Alias] = run
	}
	return loadedCertification{
		authority: authority, manifest: manifest, runs: runs,
		authoritySHA: authoritySHA, manifestSHA: hashBytes(manifestRaw),
	}, nil
}

func readPrivateJSON[T any](path string) (T, []byte, error) {
	var zero T
	info, err := os.Lstat(path)
	if err != nil {
		return zero, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maxPrivateDocumentBytes {
		return zero, nil, fmt.Errorf("private document must be a non-empty regular file of at most %d bytes with mode 0600 or stricter", maxPrivateDocumentBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return zero, nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return zero, nil, fmt.Errorf("private document identity changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxPrivateDocumentBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxPrivateDocumentBytes {
		return zero, nil, fmt.Errorf("read bounded private document")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return zero, nil, fmt.Errorf("private document has trailing JSON")
	}
	return value, raw, nil
}

func hashBytes(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
