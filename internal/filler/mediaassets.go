package filler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	MediaAssetRootName        = ".loomarr-media"
	mediaMasterDirName        = "masters"
	mediaEvidenceDirName      = "evidence"
	mediaAssetManifestVersion = 1
)

type MediaAssetRole string

const (
	MediaAssetSourceMaster MediaAssetRole = "source_master"
	MediaAssetEvidence     MediaAssetRole = "evidence"
	MediaAssetPlayback     MediaAssetRole = "playback"
)

// MediaAssetIdentity identifies bytes independently of their catalog path. SHA256 is always the
// complete-file digest; ClipHash retains the bounded sparse identity only to bind the intake row.
type MediaAssetIdentity struct {
	Role     MediaAssetRole `json:"role"`
	SHA256   string         `json:"sha256"`
	Bytes    int64          `json:"bytes"`
	ClipHash string         `json:"clipHash"`
	Path     string         `json:"path"`
}

// MediaAssetManifest is the closed portable relationship between one source and its renditions.
// Derivative execution details are added by the recipe layer; source retention can ship first
// without inventing an evidence/playback identity that does not exist yet.
type MediaAssetManifest struct {
	Version      int                     `json:"version"`
	SourceMaster MediaAssetIdentity      `json:"sourceMaster"`
	Evidence     *MediaDerivativeLineage `json:"evidence,omitempty"`
	Playback     *MediaDerivativeLineage `json:"playback,omitempty"`
}

// MediaDerivativeLineage proves which source, recipe and executable produced one rendition.
type MediaDerivativeLineage struct {
	Asset        MediaAssetIdentity           `json:"asset"`
	InputSHA256  string                       `json:"inputSha256"`
	Recipe       mediatools.DerivativeRecipe  `json:"recipe"`
	RecipeSHA256 string                       `json:"recipeSha256"`
	Tool         mediatools.MediaToolIdentity `json:"tool"`
	DurationMs   int64                        `json:"durationMs"`
	Quality      MediaQuality                 `json:"quality"`
	QC           mediatools.DerivativeQC      `json:"qc"`
	InputProbe   Probed                       `json:"inputProbe"`
	OutputProbe  Probed                       `json:"outputProbe"`
}

func (m MediaAssetManifest) validate() error {
	if m.Version != mediaAssetManifestVersion {
		return fmt.Errorf("media asset manifest version %d is unsupported", m.Version)
	}
	if err := validateMediaAssetIdentity(m.SourceMaster, MediaAssetSourceMaster, mediaMasterDirName); err != nil {
		return err
	}
	if m.Evidence != nil {
		if err := validateMediaDerivative(*m.Evidence, MediaAssetEvidence, mediaEvidenceDirName, m.SourceMaster.SHA256); err != nil {
			return err
		}
	}
	if m.Playback != nil {
		if err := validateMediaDerivative(*m.Playback, MediaAssetPlayback, "", m.SourceMaster.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func validateMediaDerivative(derivative MediaDerivativeLineage, role MediaAssetRole, tree, inputSHA256 string) error {
	if err := validateMediaAssetIdentity(derivative.Asset, role, tree); err != nil {
		return err
	}
	if derivative.InputSHA256 != inputSHA256 || !isContentHash(derivative.RecipeSHA256) || derivative.DurationMs <= 0 {
		return fmt.Errorf("%s derivative lineage is incomplete", role)
	}
	wantRecipeRole := mediatools.DerivativeEvidence
	if role == MediaAssetPlayback {
		wantRecipeRole = mediatools.DerivativePlayback
	}
	computedRecipeDigest, err := derivative.Recipe.Digest()
	if err != nil || derivative.Recipe.Role != wantRecipeRole || computedRecipeDigest != derivative.RecipeSHA256 {
		return fmt.Errorf("%s derivative recipe identity is invalid", role)
	}
	if derivative.InputProbe.DurationMs <= 0 || derivative.OutputProbe.DurationMs != derivative.DurationMs ||
		derivative.InputProbe.Height <= 0 || derivative.OutputProbe.Height <= 0 {
		return fmt.Errorf("%s derivative probe evidence is incomplete", role)
	}
	if err := derivative.Tool.Validate(); err != nil {
		return fmt.Errorf("%s derivative tool identity: %w", role, err)
	}
	if err := mediatools.ValidateDerivativeQC(derivative.QC, derivative.DurationMs,
		derivative.Recipe.KeyframeSeconds, !derivative.OutputProbe.Silent, derivative.Recipe.TargetLUFS); err != nil {
		return fmt.Errorf("%s derivative QC: %w", role, err)
	}
	return nil
}

func validateMediaAssetIdentity(asset MediaAssetIdentity, role MediaAssetRole, tree string) error {
	if asset.Role != role || !isContentHash(asset.SHA256) || !isContentHash(asset.ClipHash) || asset.Bytes <= 0 {
		return fmt.Errorf("%s media asset identity is invalid", role)
	}
	clean := filepath.Clean(filepath.FromSlash(asset.Path))
	if filepath.IsAbs(clean) || !manifestRelativePath(clean) {
		return fmt.Errorf("%s media asset path is outside the filler root", role)
	}
	if tree == "" {
		if pathContains(MediaAssetRootName, clean) {
			return fmt.Errorf("%s media asset path is hidden rather than playable", role)
		}
		return nil
	}
	wantPrefix := filepath.Join(MediaAssetRootName, tree)
	if clean == wantPrefix || !pathContains(wantPrefix, clean) {
		return fmt.Errorf("%s media asset path is outside its hidden tree", role)
	}
	return nil
}

// preserveSourceMaster snapshots the exact transcode input before any derivative is built. The
// returned hidden pathname is the stable input for all later recipes, so a concurrent mutation of
// the visible name cannot mix bytes between evidence and playback.
func preserveSourceMaster(ctx context.Context, clipDir, sourcePath, clipHash string, tags SidecarTags) (MediaAssetIdentity, error) {
	if err := ctx.Err(); err != nil {
		return MediaAssetIdentity{}, err
	}
	if clipDir == "" || !filepath.IsAbs(clipDir) || !filepath.IsAbs(sourcePath) || !isContentHash(clipHash) {
		return MediaAssetIdentity{}, errors.New("preserve source master: absolute roots and a content hash are required")
	}
	rel, err := filepath.Rel(clipDir, sourcePath)
	if err != nil || !manifestRelativePath(rel) {
		return MediaAssetIdentity{}, errors.New("preserve source master: source is outside the filler root")
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: inspect source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return MediaAssetIdentity{}, errors.New("preserve source master: source is not a non-empty regular file")
	}

	stageDir := filepath.Join(clipDir, MediaAssetRootName, ".staging")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: create staging directory: %w", err)
	}
	stage, err := os.CreateTemp(stageDir, "master-*")
	if err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: create staging file: %w", err)
	}
	stagePath := stage.Name()
	if err := stage.Close(); err != nil {
		_ = os.Remove(stagePath)
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: close staging file: %w", err)
	}
	_ = os.Remove(stagePath)
	defer func() { _ = os.Remove(stagePath) }()
	if err := snapshotOwnedFile(ctx, sourcePath, stagePath); err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: snapshot source: %w", err)
	}
	digest, size, err := FileSHA256(stagePath)
	if err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: digest snapshot: %w", err)
	}
	snapshotClipHash, err := ClipID(stagePath)
	if err != nil || snapshotClipHash != clipHash {
		return MediaAssetIdentity{}, errors.New("preserve source master: snapshot does not match catalog identity")
	}
	ext := retainedMediaExtension(sourcePath)
	relMaster := filepath.ToSlash(filepath.Join(MediaAssetRootName, mediaMasterDirName, digest[:2], digest[2:4], digest+ext))
	masterPath := filepath.Join(clipDir, filepath.FromSlash(relMaster))
	if err := os.MkdirAll(filepath.Dir(masterPath), 0o755); err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: create master directory: %w", err)
	}
	if err := publishRetainedMaster(ctx, stagePath, masterPath, digest, size); err != nil {
		return MediaAssetIdentity{}, err
	}
	asset := MediaAssetIdentity{
		Role: MediaAssetSourceMaster, SHA256: digest, Bytes: size,
		ClipHash: clipHash, Path: relMaster,
	}
	manifest := MediaAssetManifest{Version: mediaAssetManifestVersion, SourceMaster: asset}
	if err := manifest.validate(); err != nil {
		return MediaAssetIdentity{}, err
	}
	if sourceSidecar := sidecarPathFor(sourcePath); sourceSidecar != sidecarPathFor(masterPath) {
		if _, err := os.Stat(sourceSidecar); err == nil {
			if err := copyFile(sourceSidecar, sidecarPathFor(masterPath)); err != nil {
				return MediaAssetIdentity{}, fmt.Errorf("preserve source master: copy portable provenance: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return MediaAssetIdentity{}, fmt.Errorf("preserve source master: inspect portable provenance: %w", err)
		}
	}
	tags.MediaAssets = &manifest
	if err := WriteSidecarTags(masterPath, tags, false); err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("preserve source master: write manifest: %w", err)
	}
	return asset, nil
}

func retainedSourceMaster(ctx context.Context, clipDir, sourcePath, clipHash string, tags SidecarTags) (MediaAssetIdentity, error) {
	if tags.MediaAssets == nil {
		return preserveSourceMaster(ctx, clipDir, sourcePath, clipHash, tags)
	}
	if err := tags.MediaAssets.validate(); err != nil {
		return MediaAssetIdentity{}, fmt.Errorf("resolve source master: %w", err)
	}
	asset := tags.MediaAssets.SourceMaster
	masterPath := filepath.Join(clipDir, filepath.FromSlash(asset.Path))
	digest, size, err := FileSHA256(masterPath)
	if err != nil || digest != asset.SHA256 || size != asset.Bytes {
		return MediaAssetIdentity{}, errors.New("resolve source master: retained bytes do not match the manifest")
	}
	masterClipHash, err := ClipID(masterPath)
	if err != nil || masterClipHash != asset.ClipHash {
		return MediaAssetIdentity{}, errors.New("resolve source master: retained sparse identity does not match the manifest")
	}
	if err := ctx.Err(); err != nil {
		return MediaAssetIdentity{}, err
	}
	return asset, nil
}

func publishRetainedMaster(ctx context.Context, staged, target, digest string, size int64) error {
	if err := os.Link(staged, target); err == nil {
		_ = os.Remove(staged)
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("preserve source master: publish: %w", err)
	}
	existingDigest, existingSize, digestErr := FileSHA256(target)
	if digestErr != nil || existingDigest != digest || existingSize != size {
		return errors.New("preserve source master: existing content-addressed master does not match")
	}
	equal, err := exactFileBytesEqual(ctx, staged, target, mediatools.ConditioningMaxSnapshotBytes)
	if err != nil || !equal {
		return errors.New("preserve source master: existing content-addressed master does not match exact bytes")
	}
	_ = os.Remove(staged)
	return nil
}

func retainedMediaExtension(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if len(ext) < 2 || len(ext) > 10 {
		return ".bin"
	}
	for _, char := range ext[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return ".bin"
		}
	}
	return ext
}
