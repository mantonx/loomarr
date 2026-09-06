package fillerreview

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// LoadPrivateSeed reads one small secret from an owner-only regular file.
// Seed files keep secret material out of process arguments and shell history.
func LoadPrivateSeed(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open private seed: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat private seed: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > 4_096 {
		return "", fmt.Errorf("private seed must be a small owner-only regular file")
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read private seed: %w", err)
	}
	seed := strings.TrimSpace(string(raw))
	if seed == "" || strings.ContainsAny(seed, "\r\n") {
		return "", fmt.Errorf("private seed must contain exactly one non-empty line")
	}
	return seed, nil
}
