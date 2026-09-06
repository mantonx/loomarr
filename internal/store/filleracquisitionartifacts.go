package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/filler"
)

const acquisitionArtifactSelect = `SELECT id, acquisition_id, source_id, provider, source_url,
	staging_path, media_path, sidecar_path, media_sha256, media_bytes, clip_hash, state,
	repair_reason, completed_at, updated_at FROM filler_acquisition_artifacts`

// UpsertAcquisitionArtifacts records one downloader result atomically. Publication may begin only
// after this returns, so partial manifest persistence can never make only part of a source visible.
func (s *sqlStore) UpsertAcquisitionArtifacts(ctx context.Context, artifacts []filler.AcquisitionArtifact) error {
	for _, artifact := range artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("validate filler acquisition artifact %q: %w", artifact.ID, err)
		}
	}
	if len(artifacts) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin filler acquisition artifact update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	query := s.ph(`INSERT INTO filler_acquisition_artifacts
		(id, acquisition_id, source_id, provider, source_url, staging_path, media_path,
		 sidecar_path, media_sha256, media_bytes, clip_hash, state, repair_reason, completed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		 acquisition_id=excluded.acquisition_id, source_id=excluded.source_id,
		 provider=excluded.provider, source_url=excluded.source_url,
		 staging_path=excluded.staging_path, media_path=excluded.media_path,
		 sidecar_path=excluded.sidecar_path, media_sha256=excluded.media_sha256,
		 media_bytes=excluded.media_bytes, clip_hash=excluded.clip_hash,
		 state=excluded.state, repair_reason=excluded.repair_reason,
		 completed_at=excluded.completed_at, updated_at=excluded.updated_at`)
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx, query,
			artifact.ID, artifact.AcquisitionID, artifact.SourceID, artifact.Provider, artifact.SourceURL,
			artifact.StagingPath, artifact.MediaPath, artifact.SidecarPath, artifact.MediaSHA256,
			artifact.MediaBytes, artifact.ClipHash, string(artifact.State), artifact.RepairReason,
			epoch(artifact.CompletedAt), epoch(artifact.UpdatedAt)); err != nil {
			return fmt.Errorf("upsert filler acquisition artifact %s: %w", artifact.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit filler acquisition artifacts: %w", err)
	}
	return nil
}

func scanAcquisitionArtifact(sc scannable) (filler.AcquisitionArtifact, error) {
	var artifact filler.AcquisitionArtifact
	var state string
	var completedAt, updatedAt int64
	if err := sc.Scan(
		&artifact.ID, &artifact.AcquisitionID, &artifact.SourceID, &artifact.Provider,
		&artifact.SourceURL, &artifact.StagingPath, &artifact.MediaPath, &artifact.SidecarPath,
		&artifact.MediaSHA256, &artifact.MediaBytes, &artifact.ClipHash, &state,
		&artifact.RepairReason, &completedAt, &updatedAt,
	); err != nil {
		return filler.AcquisitionArtifact{}, err
	}
	artifact.State = filler.AcquisitionArtifactState(state)
	artifact.CompletedAt = fromEpoch(completedAt)
	artifact.UpdatedAt = fromEpoch(updatedAt)
	return artifact, nil
}

// AcquisitionArtifactForClip resolves ownership from the intended watch path, its paired sidecar,
// or the catalog hash bound before intake moves the file. The sidecar path deliberately retains
// the original publication name until successful consumption; it is therefore the durable retry
// binding when intake has recorded the destination but the filesystem move then fails. Newest wins
// only to tolerate an operator deliberately reusing a filename across separate acquisition runs.
func (s *sqlStore) AcquisitionArtifactForClip(
	ctx context.Context,
	mediaPath, clipHash string,
) (filler.AcquisitionArtifact, bool, error) {
	sidecarPath := strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath)) + ".info.json"
	artifact, err := scanAcquisitionArtifact(s.db.QueryRowContext(ctx, s.ph(acquisitionArtifactSelect+`
		WHERE media_path = ? OR sidecar_path = ? OR (? <> '' AND clip_hash = ?)
		ORDER BY updated_at DESC, id DESC LIMIT 1`), mediaPath, sidecarPath, clipHash, clipHash))
	if errors.Is(err, sql.ErrNoRows) {
		return filler.AcquisitionArtifact{}, false, nil
	}
	if err != nil {
		return filler.AcquisitionArtifact{}, false, fmt.Errorf("resolve filler acquisition artifact: %w", err)
	}
	return artifact, true, nil
}

// ListRecoverableAcquisitionArtifacts returns bounded non-consumed rows for startup and explicit
// repair. It includes repair rows because they retain bytes and an actionable reason.
func (s *sqlStore) ListRecoverableAcquisitionArtifacts(ctx context.Context, limit int) ([]filler.AcquisitionArtifact, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	states := []string{string(filler.ArtifactStaged), string(filler.ArtifactPublished), string(filler.ArtifactRepair)}
	rows, err := s.db.QueryContext(ctx, s.ph(acquisitionArtifactSelect+`
		WHERE state IN (?, ?, ?) ORDER BY updated_at, id LIMIT ?`), states[0], states[1], states[2], limit)
	if err != nil {
		return nil, fmt.Errorf("list recoverable filler acquisition artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	artifacts := make([]filler.AcquisitionArtifact, 0)
	for rows.Next() {
		artifact, err := scanAcquisitionArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recoverable filler acquisition artifact: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list recoverable filler acquisition artifacts: %w", err)
	}
	return artifacts, nil
}
