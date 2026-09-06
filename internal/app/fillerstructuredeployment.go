package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowopenrouter"
)

const maximumWindowStructureDeploymentBytes int64 = 1 << 20

func loadWindowStructureDeployment(path string, authority *fillerstructurewindow.MaterializationAuthority) (*fillerstructurewindowopenrouter.Deployment, error) {
	if path == "" {
		return nil, nil
	}
	if authority == nil {
		return nil, errors.New("long-reel deployment requires a valid materialization authority")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("long-reel deployment path must be clean and absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumWindowStructureDeploymentBytes {
		return nil, errors.New("long-reel deployment must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open long-reel deployment: %w", err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, maximumWindowStructureDeploymentBytes+1))
	decoder.DisallowUnknownFields()
	var deployment fillerstructurewindowopenrouter.Deployment
	if err := decoder.Decode(&deployment); err != nil {
		return nil, fmt.Errorf("decode long-reel deployment: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("long-reel deployment contains trailing JSON")
	}
	if err := fillerstructurewindowopenrouter.ValidateDeployment(deployment, *authority); err != nil {
		return nil, err
	}
	return &deployment, nil
}
