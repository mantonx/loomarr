package fillersafetycert

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maximumAuthoritySeedBytes = 1 << 10

type loadedAuthorityInputs struct {
	draft       AuthorityDraft
	draftSHA    string
	first       AuthorityReview
	second      AuthorityReview
	adjudicator *AuthorityReview
	seed        []byte
	sourceRoot  string
}

func loadAuthorityInputs(config AuthorityBuildConfig) (loadedAuthorityInputs, error) {
	paths := []string{config.DraftPath, config.FirstReviewPath, config.SecondReviewPath, config.SeedPath, config.OutputPath}
	if config.AdjudicatorPath != "" {
		paths = append(paths, config.AdjudicatorPath)
	}
	if config.ExpectedCases <= 0 || config.MaximumSourceBytes <= 0 || config.AuthoredAt.IsZero() || config.ValidateEvidence == nil ||
		strings.TrimSpace(config.SourceRoot) == "" || slicesContainBlank(paths) {
		return loadedAuthorityInputs{}, fmt.Errorf("authority build requires draft, two reviews, seed, source root, fixed authoring time, rights and provenance validation, positive ceilings, and output")
	}
	if err := requireDistinctPaths(paths); err != nil {
		return loadedAuthorityInputs{}, err
	}
	draft, draftRaw, err := readPrivateJSON[AuthorityDraft](config.DraftPath)
	if err != nil {
		return loadedAuthorityInputs{}, fmt.Errorf("read authority draft: %w", err)
	}
	first, _, err := readPrivateJSON[AuthorityReview](config.FirstReviewPath)
	if err != nil {
		return loadedAuthorityInputs{}, fmt.Errorf("read first authority review: %w", err)
	}
	second, _, err := readPrivateJSON[AuthorityReview](config.SecondReviewPath)
	if err != nil {
		return loadedAuthorityInputs{}, fmt.Errorf("read second authority review: %w", err)
	}
	var adjudicator *AuthorityReview
	if config.AdjudicatorPath != "" {
		value, _, err := readPrivateJSON[AuthorityReview](config.AdjudicatorPath)
		if err != nil {
			return loadedAuthorityInputs{}, fmt.Errorf("read authority adjudication: %w", err)
		}
		adjudicator = &value
	}
	seed, err := readPrivateRaw(config.SeedPath, maximumAuthoritySeedBytes)
	if err != nil || len(seed) < sha256.Size {
		return loadedAuthorityInputs{}, fmt.Errorf("read authority seed: private seed must contain 32 to %d bytes", maximumAuthoritySeedBytes)
	}
	root, err := filepath.EvalSymlinks(config.SourceRoot)
	if err != nil {
		return loadedAuthorityInputs{}, fmt.Errorf("resolve source root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return loadedAuthorityInputs{}, fmt.Errorf("resolve source root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return loadedAuthorityInputs{}, fmt.Errorf("source root must be a directory")
	}
	return loadedAuthorityInputs{
		draft: draft, draftSHA: hashBytes(draftRaw), first: first, second: second,
		adjudicator: adjudicator, seed: seed, sourceRoot: root,
	}, nil
}

func slicesContainBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func requireDistinctPaths(paths []string) error {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve authority path: %w", err)
		}
		if _, duplicate := seen[absolute]; duplicate {
			return fmt.Errorf("authority inputs and output must use distinct paths")
		}
		seen[absolute] = struct{}{}
	}
	return nil
}

func readPrivateRaw(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("private file must be non-empty, regular, bounded, and mode 0600 or stricter")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("private file identity changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maximum {
		return nil, fmt.Errorf("read bounded private file")
	}
	return raw, nil
}

func resolvePrivateRelative(root, relative string) (string, error) {
	if relative == "" || strings.Contains(relative, "\\") || !filepath.IsLocal(relative) || filepath.Clean(relative) != relative {
		return "", fmt.Errorf("unsafe private relative path")
	}
	joined := filepath.Join(root, relative)
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if resolved != joined {
		return "", fmt.Errorf("private path traverses a symlink")
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("private path escapes source root")
	}
	return resolved, nil
}

func hashPrivateEvidence(root, relative string) (string, error) {
	raw, err := readPrivateEvidence(root, relative)
	if err != nil {
		return "", err
	}
	return hashBytes(raw), nil
}

func readPrivateEvidence(root, relative string) ([]byte, error) {
	path, err := resolvePrivateRelative(root, relative)
	if err != nil {
		return nil, err
	}
	raw, err := readPrivateRaw(path, maxPrivateDocumentBytes)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
