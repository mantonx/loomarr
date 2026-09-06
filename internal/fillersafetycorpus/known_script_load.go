package fillersafetycorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type loadedKnownScript struct {
	authority       KnownScriptAuthority
	authorityRaw    []byte
	authoritySHA256 string
	seed            []byte
	root            string
}

func loadKnownScript(config PrepareKnownScriptConfig) (loadedKnownScript, error) {
	authority, raw, err := readPrivateJSON[KnownScriptAuthority](config.AuthorityPath, maximumReleaseAuthorityBytes)
	if err != nil {
		return loadedKnownScript{}, fmt.Errorf("read known-script authority")
	}
	seed, err := readPrivateBytes(config.SeedPath, maximumSelectionSeedBytes)
	if err != nil || len(seed) < sha256.Size {
		return loadedKnownScript{}, fmt.Errorf("read known-script alias seed: private seed must contain 32 to %d bytes", maximumSelectionSeedBytes)
	}
	root, err := filepath.Abs(filepath.Clean(config.SourceRoot))
	if err != nil {
		return loadedKnownScript{}, fmt.Errorf("resolve known-script source root")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return loadedKnownScript{}, fmt.Errorf("known-script source root must be a private non-symlink directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return loadedKnownScript{}, fmt.Errorf("resolve known-script source root")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return loadedKnownScript{}, fmt.Errorf("resolve known-script source root")
	}
	outputParent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(config.OutputDirectory)))
	if err != nil {
		return loadedKnownScript{}, fmt.Errorf("resolve known-script output parent")
	}
	outputParent, err = filepath.Abs(outputParent)
	if err != nil {
		return loadedKnownScript{}, fmt.Errorf("resolve known-script output parent")
	}
	output := filepath.Join(outputParent, filepath.Base(filepath.Clean(config.OutputDirectory)))
	if pathInside(root, output) {
		return loadedKnownScript{}, fmt.Errorf("known-script output must be outside the resolved private source root")
	}
	return loadedKnownScript{
		authority: authority, authorityRaw: raw, authoritySHA256: hashBytes(raw), seed: seed, root: root,
	}, nil
}

func decodeKnownScriptJSON[T any](raw []byte) (T, error) {
	var zero T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return zero, fmt.Errorf("private known-script document has trailing JSON")
	}
	return value, nil
}
