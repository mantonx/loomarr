package fillerquarantine

import (
	"context"
	"crypto/md5"  // #nosec G501 -- source metadata may carry an MD5 identity that must be rechecked.
	"crypto/sha1" // #nosec G505 -- source metadata may carry a SHA-1 identity that must be rechecked.
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func resolveBeneath(root, relative string) (string, error) {
	if root == "" || relative == "" || filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return "", fmt.Errorf("root and relative path are required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	candidate := filepath.Join(rootReal, filepath.FromSlash(relative))
	rel, err := filepath.Rel(rootReal, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	resolvedRel, err := filepath.Rel(rootReal, resolved)
	if err != nil || resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root through symlink")
	}
	return resolved, nil
}

type fileHashes struct {
	sha256 string
	sha1   string
	md5    string
}

func hashFile(ctx context.Context, path string) (fileHashes, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileHashes{}, 0, err
	}
	defer func() { _ = file.Close() }()
	sha256Hash := sha256.New()
	sha1Hash := sha1.New() // #nosec G401 -- compatibility identity, not a security decision.
	md5Hash := md5.New()   // #nosec G401 -- compatibility identity, not a security decision.
	bytes, err := io.Copy(io.MultiWriter(sha256Hash, sha1Hash, md5Hash), contextReader{ctx: ctx, reader: file})
	if err != nil {
		return fileHashes{}, 0, err
	}
	return fileHashes{
		sha256: hex.EncodeToString(sha256Hash.Sum(nil)),
		sha1:   hex.EncodeToString(sha1Hash.Sum(nil)),
		md5:    hex.EncodeToString(md5Hash.Sum(nil)),
	}, bytes, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(buffer)
	}
}
