package fillersafetycorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type privateStage struct {
	path      string
	target    string
	published bool
}

func beginPrivateStage(target string) (*privateStage, error) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(absolute); err == nil || !os.IsNotExist(err) {
		return nil, fmt.Errorf("private output already exists or cannot be inspected")
	}
	parent := filepath.Dir(absolute)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("private output parent must exist")
	}
	path, err := os.MkdirTemp(parent, ".filler-safety-stage-")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		_ = os.RemoveAll(path)
		return nil, err
	}
	return &privateStage{path: path, target: absolute}, nil
}

func (stage *privateStage) cleanup() {
	if stage != nil && !stage.published {
		_ = os.RemoveAll(stage.path)
	}
}

func (stage *privateStage) publish() error {
	if stage == nil || stage.published {
		return fmt.Errorf("private stage is invalid")
	}
	if err := os.Rename(stage.path, stage.target); err != nil {
		return err
	}
	stage.published = true
	return nil
}

func writePrivateJSON(path string, value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if err := writePrivate(path, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func writePrivate(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	written = true
	return nil
}
