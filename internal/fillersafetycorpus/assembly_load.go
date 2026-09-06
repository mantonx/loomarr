package fillersafetycorpus

import (
	"fmt"
	"os"
	"path/filepath"
)

func loadAssemblyPlan(config ReviewDraftConfig) (AssemblyPlan, []byte, string, error) {
	plan, raw, err := readPrivateJSON[AssemblyPlan](config.PlanPath, maximumAssemblyPlanBytes)
	if err != nil {
		return AssemblyPlan{}, nil, "", fmt.Errorf("read spoken corpus assembly plan: %w", err)
	}
	root, err := resolvePrivateInputRoot(config.InputRoot)
	if err != nil {
		return AssemblyPlan{}, nil, "", err
	}
	return plan, raw, root, nil
}

func resolvePrivateInputRoot(value string) (string, error) {
	root, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", fmt.Errorf("resolve spoken corpus input root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve spoken corpus input root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("spoken corpus input root must be a private directory")
	}
	return root, nil
}

func resolvePrivateAssemblyDirectory(root, relative string) (string, error) {
	if !validRelative(relative) {
		return "", fmt.Errorf("spoken corpus source root is invalid")
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil || resolved != path {
		return "", fmt.Errorf("spoken corpus source root traverses a symlink")
	}
	if relativeToInput, err := filepath.Rel(root, resolved); err != nil || !filepath.IsLocal(relativeToInput) {
		return "", fmt.Errorf("spoken corpus source root escapes the input root")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("spoken corpus source root must be a private directory")
	}
	return resolved, nil
}

func requirePrivateAssemblyFile(root string, authority FileAuthority, maximum int64) error {
	path, err := verifiedMemberPath(root, authority, maximum)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("spoken corpus input must be a private regular file")
	}
	return nil
}

func readPrivateAssemblyFile(root string, authority FileAuthority, maximum int64) ([]byte, error) {
	if err := requirePrivateAssemblyFile(root, authority, maximum); err != nil {
		return nil, err
	}
	return readVerifiedMember(root, authority, maximum)
}

func snapshotPrivateAssemblyFile(root string, authority FileAuthority, output string, maximum int64) error {
	if err := requirePrivateAssemblyFile(root, authority, maximum); err != nil {
		return err
	}
	return snapshotVerifiedMember(root, authority, output, maximum)
}

func readPrivateAssemblyJSON[T any](root, relative, expectedSHA string, maximum int64) (T, []byte, error) {
	var zero T
	if !validRelative(relative) || !validSHA256(expectedSHA) || maximum <= 0 {
		return zero, nil, fmt.Errorf("spoken corpus document authority is invalid")
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maximum {
		return zero, nil, fmt.Errorf("spoken corpus document must be a bounded private regular file")
	}
	authority := FileAuthority{Path: relative, SHA256: expectedSHA, Bytes: info.Size()}
	if err := requirePrivateAssemblyFile(root, authority, maximum); err != nil {
		return zero, nil, err
	}
	value, raw, err := readPrivateJSON[T](path, maximum)
	if err != nil || hashBytes(raw) != expectedSHA {
		return zero, nil, fmt.Errorf("spoken corpus document bytes or schema are invalid")
	}
	return value, raw, nil
}
