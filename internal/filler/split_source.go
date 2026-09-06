package filler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type SplitSourceRole string

const (
	SplitSourceEvidence       SplitSourceRole = "evidence"
	SplitSourceLegacyPlayback SplitSourceRole = "legacy_playback"
)

// SplitSourceAsset freezes the exact bytes and timeline on which a cut proposal is based. A
// playback recipe may change later without rewriting the meaning of an existing review.
type SplitSourceAsset struct {
	Role       SplitSourceRole `json:"role"`
	SHA256     string          `json:"sha256"`
	Bytes      int64           `json:"bytes"`
	ClipHash   string          `json:"clipHash"`
	Path       string          `json:"path"`
	DurationMs int64           `json:"durationMs"`
}

func (s SplitSourceAsset) empty() bool {
	return s == (SplitSourceAsset{})
}

func (s SplitSourceAsset) validate() error {
	if s.Role != SplitSourceEvidence && s.Role != SplitSourceLegacyPlayback {
		return fmt.Errorf("split source role %q is unsupported", s.Role)
	}
	if !isContentHash(s.SHA256) || !isContentHash(s.ClipHash) || s.Bytes <= 0 || s.DurationMs <= 0 || !manifestRelativePath(filepath.FromSlash(s.Path)) {
		return errors.New("split source identity is incomplete")
	}
	clean := filepath.Clean(filepath.FromSlash(s.Path))
	if s.Role == SplitSourceEvidence {
		if !pathContains(filepath.Join(MediaAssetRootName, mediaEvidenceDirName), clean) {
			return errors.New("split evidence source is outside the evidence tree")
		}
	} else if pathContains(MediaAssetRootName, clean) {
		return errors.New("legacy split source is unexpectedly hidden")
	}
	return nil
}

func resolveSplitSource(ctx context.Context, clipDir string, clip StoreClip, bound SplitSourceAsset) (SplitSourceAsset, string, error) {
	if bound.empty() {
		playable := filepath.Join(clipDir, filepath.FromSlash(clip.Path))
		if tags, ok := ReadSidecarTags(playable); ok && tags.MediaAssets != nil && tags.MediaAssets.Evidence != nil {
			evidence := tags.MediaAssets.Evidence
			bound = SplitSourceAsset{
				Role: SplitSourceEvidence, SHA256: evidence.Asset.SHA256, Bytes: evidence.Asset.Bytes,
				ClipHash: evidence.Asset.ClipHash, Path: evidence.Asset.Path, DurationMs: evidence.DurationMs,
			}
		} else {
			digest, size, err := FileSHA256(playable)
			if err != nil {
				return SplitSourceAsset{}, "", fmt.Errorf("resolve legacy split source: %w", err)
			}
			bound = SplitSourceAsset{
				Role: SplitSourceLegacyPlayback, SHA256: digest, Bytes: size,
				ClipHash: clip.Hash, Path: clip.Path, DurationMs: clip.DurationMs,
			}
		}
	}
	if err := bound.validate(); err != nil {
		return SplitSourceAsset{}, "", err
	}
	full := filepath.Join(clipDir, filepath.FromSlash(bound.Path))
	info, err := os.Lstat(full)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return SplitSourceAsset{}, "", errors.New("split source is not a regular retained artifact")
	}
	digest, size, err := FileSHA256(full)
	if err != nil || digest != bound.SHA256 || size != bound.Bytes {
		return SplitSourceAsset{}, "", errors.New("split source bytes do not match the proposal")
	}
	sparse, err := ClipID(full)
	if err != nil || sparse != bound.ClipHash {
		return SplitSourceAsset{}, "", errors.New("split source sparse identity does not match the proposal")
	}
	if err := ctx.Err(); err != nil {
		return SplitSourceAsset{}, "", err
	}
	return bound, full, nil
}
