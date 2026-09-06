package filler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/loomarr/loomarr/internal/mediatools"
)

type conditioningSnapshots struct {
	Source string
	Parent string
}

// snapshotConditioningArtifacts binds the child and retained parent to private complete-byte
// snapshots before any measurement or rewrite. The caller sees one operation and cannot forget
// either identity or exact-byte check.
func snapshotConditioningArtifacts(ctx context.Context, stageDir, source, parent, sourceHash, parentHash string) (conditioningSnapshots, error) {
	out := conditioningSnapshots{
		Source: filepath.Join(stageDir, "source"+filepath.Ext(source)),
		Parent: filepath.Join(stageDir, "parent"+filepath.Ext(parent)),
	}
	cleanup := func() { _ = os.Remove(out.Source); _ = os.Remove(out.Parent) }
	if err := snapshotOwnedFile(ctx, source, out.Source); err != nil {
		cleanup()
		return conditioningSnapshots{}, errors.New("conditioning source could not be snapshotted")
	}
	if err := snapshotOwnedFile(ctx, parent, out.Parent); err != nil {
		cleanup()
		return conditioningSnapshots{}, errors.New("conditioning parent could not be snapshotted")
	}
	sourceID, sourceErr := ClipID(out.Source)
	parentID, parentErr := ClipID(out.Parent)
	if sourceErr != nil || parentErr != nil || sourceID != sourceHash || parentID != parentHash {
		cleanup()
		return conditioningSnapshots{}, errors.New("conditioning source or parent bytes do not match catalog identity")
	}
	sourceEqual, sourceErr := exactFileBytesEqual(ctx, source, out.Source, mediatools.ConditioningMaxSnapshotBytes)
	parentEqual, parentErr := exactFileBytesEqual(ctx, parent, out.Parent, mediatools.ConditioningMaxSnapshotBytes)
	if sourceErr != nil || parentErr != nil || !sourceEqual || !parentEqual {
		cleanup()
		return conditioningSnapshots{}, errors.New("conditioning source or parent snapshot is not exact")
	}
	return out, nil
}

// validateRecoveredTranscode proves an existing canonical artifact is the exact output and restart
// record from this attempt. Sparse catalog identity alone is never recovery authority.
func validateRecoveredTranscode(ctx context.Context, staged, published, expectedHash, profileID string, lineage *ConditioningLineage, evidence *ConditioningEvidence, quality MediaQuality, normalizedLUFS float64, assets *MediaAssetManifest) error {
	publishedID, err := ClipID(published)
	if err != nil || publishedID != expectedHash {
		return errors.New("existing transformed artifact bytes do not match their identity")
	}
	equal, err := exactFileBytesEqual(ctx, staged, published, mediatools.ConditioningMaxSnapshotBytes)
	if err != nil || !equal {
		return errors.New("existing transformed artifact bytes do not exactly match the staged output")
	}
	tags, ok := ReadSidecarTags(published)
	if !ok || tags.Mezzanine != profileID {
		return errors.New("existing transformed artifact has missing or corrupt evidence")
	}
	if assets != nil && !reflect.DeepEqual(tags.MediaAssets, assets) {
		return errors.New("existing transformed artifact is not bound to this media asset manifest")
	}
	if evidence != nil && (!reflect.DeepEqual(tags.ConditioningLineage, lineage) ||
		tags.Conditioning == nil || !reflect.DeepEqual(*tags.Conditioning, *evidence) ||
		tags.MediaQuality == nil || !reflect.DeepEqual(*tags.MediaQuality, quality) ||
		tags.NormalizedLUFS != normalizedLUFS) {
		return errors.New("existing transformed artifact is not bound to this child evidence")
	}
	return nil
}

// publishTranscodeReplacement owns the final sidecar/media visibility ordering for one transformed
// artifact. The staged pair remains private until this operation succeeds.
func publishTranscodeReplacement(staged, published, original string, tags SidecarTags, alreadyPublished bool) error {
	if err := os.MkdirAll(filepath.Dir(published), 0o755); err != nil {
		return fmt.Errorf("create content shard: %w", err)
	}
	if published == original {
		return WriteSidecarTags(original, tags, false)
	}
	if alreadyPublished {
		return nil
	}
	return publishHiddenMediaPair(staged, published)
}
