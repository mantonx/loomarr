package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

const acquisitionRunSelect = `SELECT id, trigger, source_id, pull_id, status,
	requested, fetched, skipped, failed, empty_count, error, started_at, completed_at, updated_at
	FROM filler_acquisition_runs`

// UpsertAcquisitionRun persists one execution snapshot. The app adapter is the single writer and
// rewrites the whole snapshot as the job moves queued -> running -> success/error.
func (s *sqlStore) UpsertAcquisitionRun(ctx context.Context, run filler.AcquisitionRun) error {
	_, err := s.db.ExecContext(ctx, s.ph(`INSERT INTO filler_acquisition_runs
		(id, trigger, source_id, pull_id, status, requested, fetched, skipped, failed,
		 empty_count, error, started_at, completed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		 trigger=excluded.trigger, source_id=excluded.source_id, pull_id=excluded.pull_id,
		 status=excluded.status, requested=excluded.requested, fetched=excluded.fetched,
		 skipped=excluded.skipped, failed=excluded.failed, empty_count=excluded.empty_count,
		 error=excluded.error, started_at=excluded.started_at,
		 completed_at=excluded.completed_at, updated_at=excluded.updated_at`),
		run.ID, string(run.Trigger), run.SourceID, run.PullID, string(run.Status),
		run.Requested, run.Fetched, run.Skipped, run.Failed, run.Empty, run.Error,
		epoch(run.StartedAt), epoch(run.CompletedAt), epoch(run.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert filler acquisition %s: %w", run.ID, err)
	}
	return nil
}

// RecoverInterruptedAcquisitionRuns closes jobs whose in-memory worker disappeared with the
// previous process. Without this, a restart turns queued/running into a permanent false promise.
func (s *sqlStore) RecoverInterruptedAcquisitionRuns(ctx context.Context, at time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, s.ph(`UPDATE filler_acquisition_runs
		SET status = ?, error = ?, completed_at = ?, updated_at = ?
		WHERE status IN (?, ?)`),
		string(filler.AcquisitionError), "application restarted before the acquisition completed",
		epoch(at), epoch(at), string(filler.AcquisitionQueued), string(filler.AcquisitionRunning))
	if err != nil {
		return 0, fmt.Errorf("recover interrupted filler acquisitions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count interrupted filler acquisitions: %w", err)
	}
	return int(n), nil
}

func scanAcquisitionRun(sc scannable) (filler.AcquisitionRun, error) {
	var run filler.AcquisitionRun
	var trigger, status string
	var startedAt, completedAt, updatedAt int64
	if err := sc.Scan(&run.ID, &trigger, &run.SourceID, &run.PullID, &status,
		&run.Requested, &run.Fetched, &run.Skipped, &run.Failed, &run.Empty, &run.Error,
		&startedAt, &completedAt, &updatedAt); err != nil {
		return filler.AcquisitionRun{}, err
	}
	run.Trigger = filler.AcquisitionTrigger(trigger)
	run.Status = filler.AcquisitionStatus(status)
	run.StartedAt = fromEpoch(startedAt)
	run.CompletedAt = fromEpoch(completedAt)
	run.UpdatedAt = fromEpoch(updatedAt)
	return run, nil
}

// GetAcquisitionRun returns one run plus its current pipeline outcomes. An unknown id is
// ErrNotFound, matching pulls and sources rather than leaking sql.ErrNoRows through the API seam.
func (s *sqlStore) GetAcquisitionRun(ctx context.Context, id string, at time.Time) (filler.AcquisitionRun, error) {
	run, err := scanAcquisitionRun(s.db.QueryRowContext(ctx, s.ph(acquisitionRunSelect+` WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return filler.AcquisitionRun{}, ErrNotFound
	}
	if err != nil {
		return filler.AcquisitionRun{}, fmt.Errorf("get filler acquisition %s: %w", id, err)
	}
	runs := []filler.AcquisitionRun{run}
	if err := s.attachAcquisitionOutcomes(ctx, runs, at); err != nil {
		return filler.AcquisitionRun{}, err
	}
	return runs[0], nil
}

// ListAcquisitionRuns returns newest first and bounded. A non-positive limit uses the UI-sized
// default; callers cannot accidentally materialise an unbounded execution history.
func (s *sqlStore) ListAcquisitionRuns(ctx context.Context, limit int, at time.Time) ([]filler.AcquisitionRun, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, s.ph(acquisitionRunSelect+` ORDER BY started_at DESC, id DESC LIMIT ?`), limit)
	if err != nil {
		return nil, fmt.Errorf("list filler acquisitions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	runs := make([]filler.AcquisitionRun, 0)
	for rows.Next() {
		run, err := scanAcquisitionRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan filler acquisition: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list filler acquisitions: %w", err)
	}
	if err := s.attachAcquisitionOutcomes(ctx, runs, at); err != nil {
		return nil, err
	}
	return runs, nil
}

// attachAcquisitionOutcomes batches pipeline attribution for the bounded run page, then delegates
// every ownership decision to filler.AcquisitionOutcomeFrom. SQL selects; the domain classifies.
func (s *sqlStore) attachAcquisitionOutcomes(ctx context.Context, runs []filler.AcquisitionRun, at time.Time) error {
	if len(runs) == 0 {
		return nil
	}
	placeholders := make([]string, len(runs))
	args := make([]any, len(runs))
	byID := make(map[string][]filler.ClipPipeline, len(runs))
	for i := range runs {
		placeholders[i] = "?"
		args[i] = runs[i].ID
	}
	rows, err := s.db.QueryContext(ctx, s.ph(clipPipelineSelect+` WHERE acquisition_id IN (`+
		strings.Join(placeholders, ",")+`) ORDER BY acquisition_id, clip_hash`), args...)
	if err != nil {
		return fmt.Errorf("list acquisition pipelines: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		row, err := scanClipPipeline(rows)
		if err != nil {
			return fmt.Errorf("scan acquisition pipeline: %w", err)
		}
		byID[row.AcquisitionID] = append(byID[row.AcquisitionID], row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list acquisition pipelines: %w", err)
	}
	for i := range runs {
		runs[i].Outcome = filler.AcquisitionOutcomeFrom(byID[runs[i].ID], at)
	}
	artifactRows, err := s.db.QueryContext(ctx, s.ph(acquisitionArtifactSelect+` WHERE acquisition_id IN (`+
		strings.Join(placeholders, ",")+`) ORDER BY acquisition_id, updated_at DESC, id DESC`), args...)
	if err != nil {
		return fmt.Errorf("list acquisition artifacts: %w", err)
	}
	defer func() { _ = artifactRows.Close() }()
	artifactsByID := make(map[string][]filler.AcquisitionArtifact, len(runs))
	for artifactRows.Next() {
		artifact, err := scanAcquisitionArtifact(artifactRows)
		if err != nil {
			return fmt.Errorf("scan acquisition artifact: %w", err)
		}
		artifactsByID[artifact.AcquisitionID] = append(artifactsByID[artifact.AcquisitionID], artifact)
	}
	if err := artifactRows.Err(); err != nil {
		return fmt.Errorf("list acquisition artifacts: %w", err)
	}
	for i := range runs {
		runs[i].Artifacts = filler.AcquisitionArtifactOutcomeFrom(artifactsByID[runs[i].ID])
	}
	return nil
}
