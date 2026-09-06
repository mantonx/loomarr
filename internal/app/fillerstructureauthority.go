package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

const maximumWindowStructureAuthorityBytes int64 = 1 << 20

// loadWindowStructureAuthority treats a configured authority as bounded immutable input. A path
// is only a locator; the validated content digest remains the policy identity.
func loadWindowStructureAuthority(path string) (*fillerstructurewindow.MaterializationAuthority, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("long-reel materialization authority path must be clean and absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumWindowStructureAuthorityBytes {
		return nil, errors.New("long-reel materialization authority must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open long-reel materialization authority: %w", err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, maximumWindowStructureAuthorityBytes+1))
	decoder.DisallowUnknownFields()
	var authority fillerstructurewindow.MaterializationAuthority
	if err := decoder.Decode(&authority); err != nil {
		return nil, fmt.Errorf("decode long-reel materialization authority: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("long-reel materialization authority contains trailing JSON")
	}
	if err := fillerstructurewindow.ValidateMaterializationAuthority(authority); err != nil {
		return nil, err
	}
	return &authority, nil
}
