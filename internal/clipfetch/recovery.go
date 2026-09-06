package clipfetch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

// ArtifactRecoveryStore is the persistence slice needed to resume staged publication.
type ArtifactRecoveryStore interface {
	ArtifactWriter
	ListRecoverableAcquisitionArtifacts(context.Context, int) ([]filler.AcquisitionArtifact, error)
}

type RecoveryResult struct {
	Published int
	Repair    int
	Pending   int
}

// RecoverAcquisitionArtifacts resumes the bounded publication protocol after restart. Repair rows
// are retained for explicit intervention; consumed rows are not returned by the store.
func RecoverAcquisitionArtifacts(
	ctx context.Context,
	watchDir string,
	clipDir string,
	store ArtifactRecoveryStore,
	now func() time.Time,
) (RecoveryResult, error) {
	var result RecoveryResult
	if store == nil || watchDir == "" {
		return result, nil
	}
	if now == nil {
		now = time.Now
	}
	artifacts, err := store.ListRecoverableAcquisitionArtifacts(ctx, 500)
	if err != nil {
		return result, err
	}
	for _, artifact := range artifacts {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		if artifact.State == filler.ArtifactRepair {
			result.Repair++
			continue
		}
		if artifact.State == filler.ArtifactPublished {
			mediaErr := verifyManifestFile(filepath.Join(watchDir, artifact.MediaPath), artifact)
			sidecarErr := verifyPortableProvenance(filepath.Join(watchDir, artifact.SidecarPath), artifact)
			if err := errors.Join(mediaErr, sidecarErr); err != nil {
				if consumed, consumedErr := recoverConsumedArtifact(clipDir, artifact, now()); consumedErr == nil {
					if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{consumed}); err != nil {
						return result, err
					}
					continue
				}
				artifact = repairArtifact(artifact, "published artifact validation: "+err.Error(), now())
				if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{artifact}); err != nil {
					return result, err
				}
				result.Repair++
			} else {
				result.Pending++ // intake owns the published -> consumed transition
			}
			continue
		}

		stageMedia := filepath.Join(watchDir, artifact.StagingPath)
		if err := verifyManifestFile(stageMedia, artifact); err != nil {
			// A crash may have happened after media publication but before its acknowledgement.
			targetMedia := filepath.Join(watchDir, artifact.MediaPath)
			targetSidecar := filepath.Join(watchDir, artifact.SidecarPath)
			if targetErr := errors.Join(verifyManifestFile(targetMedia, artifact), verifyPortableProvenance(targetSidecar, artifact)); targetErr == nil {
				artifact.State = filler.ArtifactPublished
				artifact.UpdatedAt = now().UTC()
				if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{artifact}); err != nil {
					return result, err
				}
				result.Pending++
				continue
			}
			if consumed, consumedErr := recoverConsumedArtifact(clipDir, artifact, now()); consumedErr == nil {
				if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{consumed}); err != nil {
					return result, err
				}
				continue
			}
			artifact = repairArtifact(artifact, "staged artifact validation: "+err.Error(), now())
			if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{artifact}); err != nil {
				return result, err
			}
			result.Repair++
			continue
		}

		stageSidecar := strings.TrimSuffix(stageMedia, filepath.Ext(stageMedia)) + ".info.json"
		targetSidecar := filepath.Join(watchDir, artifact.SidecarPath)
		if err := recoverSidecar(stageSidecar, targetSidecar, artifact); err != nil {
			artifact = repairArtifact(artifact, "staged sidecar validation: "+err.Error(), now())
			if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{artifact}); err != nil {
				return result, err
			}
			result.Repair++
			continue
		}
		if err := publishFile(stageMedia, filepath.Join(watchDir, artifact.MediaPath)); err != nil {
			artifact = repairArtifact(artifact, "resume media publication: "+err.Error(), now())
			if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{artifact}); err != nil {
				return result, err
			}
			result.Repair++
			continue
		}
		artifact.State = filler.ArtifactPublished
		artifact.UpdatedAt = now().UTC()
		if err := store.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{artifact}); err != nil {
			return result, err
		}
		result.Published++
	}
	return result, nil
}

func recoverConsumedArtifact(clipDir string, artifact filler.AcquisitionArtifact, at time.Time) (filler.AcquisitionArtifact, error) {
	if clipDir == "" || artifact.ClipHash == "" {
		return artifact, errors.New("consumed location unavailable")
	}
	media, err := filler.ClipPath(clipDir, artifact.ClipHash, filepath.Ext(artifact.MediaPath))
	if err != nil {
		return artifact, err
	}
	if err := verifyManifestFile(media, artifact); err != nil {
		return artifact, err
	}
	sidecar := strings.TrimSuffix(media, filepath.Ext(media)) + ".info.json"
	if err := verifyPortableProvenance(sidecar, artifact); err != nil {
		return artifact, err
	}
	relative, err := filepath.Rel(clipDir, media)
	if err != nil || !withinDir(relative) {
		return artifact, errors.New("consumed artifact escaped clip root")
	}
	artifact.State = filler.ArtifactConsumed
	artifact.MediaPath = filepath.ToSlash(relative)
	artifact.SidecarPath = strings.TrimSuffix(artifact.MediaPath, filepath.Ext(artifact.MediaPath)) + ".info.json"
	artifact.RepairReason = ""
	artifact.UpdatedAt = at.UTC()
	return artifact, nil
}

func verifyManifestFile(path string, artifact filler.AcquisitionArtifact) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("media is symlinked or not a regular file")
	}
	digest, size, err := filler.FileSHA256(path)
	if err != nil {
		return err
	}
	if digest != artifact.MediaSHA256 || size != artifact.MediaBytes {
		return errors.New("media digest or size changed")
	}
	return nil
}

func recoverSidecar(stagePath, targetPath string, artifact filler.AcquisitionArtifact) error {
	if targetPath == "" {
		return errors.New("manifest has no sidecar path")
	}
	if _, err := os.Lstat(targetPath); err == nil {
		return verifyPortableProvenance(targetPath, artifact)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := verifyPortableProvenance(stagePath, artifact); err != nil {
		return err
	}
	return publishFile(stagePath, targetPath)
}

func verifyPortableProvenance(path string, artifact filler.AcquisitionArtifact) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("sidecar is symlinked or not a regular file")
	}
	tags, state := filler.ReadSidecarTagsState(strings.TrimSuffix(path, ".info.json") + filepath.Ext(artifact.MediaPath))
	if state != filler.SidecarValid || tags.AcquisitionID != artifact.AcquisitionID || tags.SourceID != artifact.SourceID {
		return fmt.Errorf("sidecar does not carry exact acquisition provenance")
	}
	mediaPath := strings.TrimSuffix(path, ".info.json") + filepath.Ext(artifact.MediaPath)
	if !filler.SidecarFetchedByUs(mediaPath) {
		return errors.New("sidecar does not mark Loomarr ownership")
	}
	return nil
}

func repairArtifact(artifact filler.AcquisitionArtifact, reason string, at time.Time) filler.AcquisitionArtifact {
	artifact.State = filler.ArtifactRepair
	artifact.RepairReason = reason
	artifact.UpdatedAt = at.UTC()
	return artifact
}
