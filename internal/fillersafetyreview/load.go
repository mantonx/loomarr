package fillersafetyreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func loadPlan(config Config) (Plan, []byte, error) {
	plan, raw, err := readPrivateJSON[Plan](config.PlanPath, maximumPlanBytes)
	if err != nil {
		return Plan{}, nil, fmt.Errorf("read spoken-safety model review plan: %w", err)
	}
	if err := validatePlan(plan); err != nil {
		return Plan{}, nil, err
	}
	return plan, raw, nil
}

func loadInputs(ctx context.Context, config Config, plan Plan, planRaw []byte) (loadedInputs, error) {
	if err := ctx.Err(); err != nil {
		return loadedInputs{}, err
	}
	root, err := privateRoot(config.InputRoot)
	if err != nil {
		return loadedInputs{}, err
	}
	draft, draftRaw, err := readRootAuthority[fillersafetycert.AuthorityDraft](root, plan.Draft, maximumDocumentBytes)
	if err != nil {
		return loadedInputs{}, fmt.Errorf("read model review draft: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return loadedInputs{}, err
	}
	worklist, worklistRaw, err := readRootAuthority[fillersafetycorpus.ReviewWorklist](root, plan.Worklist, maximumDocumentBytes)
	if err != nil {
		return loadedInputs{}, fmt.Errorf("read model review worklist: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return loadedInputs{}, err
	}
	snapshot, snapshotRaw, err := readRootAuthority[fillerbakeoff.OpenRouterSnapshot](root, plan.Snapshot, maximumDocumentBytes)
	if err != nil {
		return loadedInputs{}, fmt.Errorf("read model review route snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return loadedInputs{}, err
	}
	policyPath, err := resolveRootPath(root, worklist.PolicyPath)
	if err != nil {
		return loadedInputs{}, fmt.Errorf("resolve model review policy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return loadedInputs{}, err
	}
	policy, policyRaw, err := readPrivateJSON[fillersafety.Policy](policyPath, maximumDocumentBytes)
	if err != nil {
		return loadedInputs{}, fmt.Errorf("read model review policy: %w", err)
	}
	return loadedInputs{
		plan: plan, planSHA256: hashBytes(planRaw), draft: draft, draftSHA256: hashBytes(draftRaw),
		worklist: worklist, worklistSHA256: hashBytes(worklistRaw), policy: policy,
		policySHA256: hashBytes(policyRaw), policyBytes: int64(len(policyRaw)),
		snapshot: snapshot, snapshotSHA256: hashBytes(snapshotRaw),
		root:       root,
		inputBytes: int64(len(planRaw) + len(draftRaw) + len(worklistRaw) + len(snapshotRaw) + len(policyRaw)),
	}, nil
}

func readRootAuthority[T any](root string, authority fillersafetycorpus.FileAuthority, maximum int64) (T, []byte, error) {
	var zero T
	if !validRelative(authority.Path) || !validSHA256(authority.SHA256) || authority.Bytes <= 0 || authority.Bytes > maximum {
		return zero, nil, fmt.Errorf("file authority is invalid")
	}
	path, err := resolveRootPath(root, authority.Path)
	if err != nil {
		return zero, nil, err
	}
	value, raw, err := readPrivateJSON[T](path, maximum)
	if err != nil {
		return zero, nil, err
	}
	if int64(len(raw)) != authority.Bytes || hashBytes(raw) != authority.SHA256 {
		return zero, nil, fmt.Errorf("file bytes do not match authority")
	}
	return value, raw, nil
}

func readPrivateJSON[T any](path string, maximum int64) (T, []byte, error) {
	var zero T
	raw, err := readPrivateFile(path, maximum)
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

func readPrivateFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("private input must be a non-empty bounded regular file at mode 0600 or stricter")
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

func privateRoot(value string) (string, error) {
	clean, err := filepath.Abs(filepath.Clean(value))
	if err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("model review input root is invalid")
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("model review input root must be a private non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("resolve model review input root: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve model review input root: %w", err)
	}
	return resolved, nil
}

func resolveRootPath(root, relative string) (string, error) {
	if !validRelative(relative) {
		return "", fmt.Errorf("private relative path is invalid")
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil || resolved != path {
		return "", fmt.Errorf("private path traverses a symlink")
	}
	if relativeToRoot, err := filepath.Rel(root, resolved); err != nil || !filepath.IsLocal(relativeToRoot) {
		return "", fmt.Errorf("private path escapes input root")
	}
	return resolved, nil
}

func hashPrivateAuthority(root string, authority fillersafetycorpus.FileAuthority, maximum int64) error {
	path, err := resolveRootPath(root, authority.Path)
	if err != nil {
		return err
	}
	raw, err := readPrivateFile(path, maximum)
	if err != nil {
		return err
	}
	if int64(len(raw)) != authority.Bytes || hashBytes(raw) != authority.SHA256 {
		return fmt.Errorf("private evidence bytes do not match authority")
	}
	return nil
}

func validRelative(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && filepath.IsLocal(value) &&
		filepath.ToSlash(filepath.Clean(value)) == value
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
