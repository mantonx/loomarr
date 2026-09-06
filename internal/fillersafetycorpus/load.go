package fillersafetycorpus

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

const (
	maximumReleaseAuthorityBytes = 64 << 20
	maximumSelectionSeedBytes    = 1 << 10
	maximumTranscriptBytes       = 1 << 20
)

type loadedVCTK struct {
	authority    VCTKReleaseAuthority
	authorityRaw []byte
	authoritySHA string
	seed         []byte
	root         string
}

func loadVCTK(config PrepareVCTKConfig) (loadedVCTK, error) {
	authority, raw, err := readPrivateJSON[VCTKReleaseAuthority](config.ReleaseAuthorityPath, maximumReleaseAuthorityBytes)
	if err != nil {
		return loadedVCTK{}, fmt.Errorf("read VCTK release authority: %w", err)
	}
	seed, err := readPrivateBytes(config.SeedPath, maximumSelectionSeedBytes)
	if err != nil || len(seed) < sha256.Size {
		return loadedVCTK{}, fmt.Errorf("read VCTK selection seed: private seed must contain 32 to %d bytes", maximumSelectionSeedBytes)
	}
	root, err := filepath.EvalSymlinks(config.ReleaseRoot)
	if err != nil {
		return loadedVCTK{}, fmt.Errorf("resolve VCTK release root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return loadedVCTK{}, fmt.Errorf("resolve VCTK release root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return loadedVCTK{}, fmt.Errorf("VCTK release root must be a directory")
	}
	return loadedVCTK{authority: authority, authorityRaw: raw, authoritySHA: hashBytes(raw), seed: seed, root: root}, nil
}

func readPrivateJSON[T any](path string, maximum int64) (T, []byte, error) {
	var zero T
	raw, err := readPrivateBytes(path, maximum)
	if err != nil {
		return zero, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return zero, nil, fmt.Errorf("private document has trailing JSON")
	}
	return value, raw, nil
}

func readPrivateBytes(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("private input must be a non-empty regular file of at most %d bytes with mode 0600 or stricter", maximum)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("private input identity changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maximum {
		return nil, fmt.Errorf("read bounded private input")
	}
	return raw, nil
}

func verifiedMemberPath(root string, authority FileAuthority, maximum int64) (string, error) {
	path, file, err := openVerifiedMember(root, authority, maximum)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || written != authority.Bytes || fmt.Sprintf("%x", hash.Sum(nil)) != authority.SHA256 {
		return "", fmt.Errorf("member bytes do not match authority")
	}
	return path, nil
}

func openVerifiedMember(root string, authority FileAuthority, maximum int64) (string, *os.File, error) {
	if !validRelative(authority.Path) || !validSHA256(authority.SHA256) || authority.Bytes <= 0 || authority.Bytes > maximum {
		return "", nil, fmt.Errorf("member authority is invalid")
	}
	path := filepath.Join(root, filepath.FromSlash(authority.Path))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil || resolved != path {
		return "", nil, fmt.Errorf("member path traverses a symlink")
	}
	if relative, err := filepath.Rel(root, resolved); err != nil || !filepath.IsLocal(relative) {
		return "", nil, fmt.Errorf("member path escapes release root")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() != authority.Bytes {
		return "", nil, fmt.Errorf("member is missing or has changed size")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return "", nil, fmt.Errorf("member identity changed while opening")
	}
	return resolved, file, nil
}

func readVerifiedMember(root string, authority FileAuthority, maximum int64) ([]byte, error) {
	_, file, err := openVerifiedMember(root, authority, maximum)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	var raw bytes.Buffer
	written, err := io.Copy(io.MultiWriter(&raw, hash), io.LimitReader(file, maximum+1))
	if err != nil || written != authority.Bytes || fmt.Sprintf("%x", hash.Sum(nil)) != authority.SHA256 {
		return nil, fmt.Errorf("member bytes do not match authority")
	}
	return raw.Bytes(), nil
}

func validRelative(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && filepath.IsLocal(value) &&
		filepath.ToSlash(filepath.Clean(value)) == value
}

func validSHA256(value string) bool {
	return len(value) == 64 && value == strings.ToLower(value) && strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdef", r)
	}) == -1
}

func hashBytes(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
