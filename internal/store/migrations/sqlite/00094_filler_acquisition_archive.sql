-- +goose Up
-- V65 (§10): retain the exact provider archive replay obligation on each owned output.

ALTER TABLE filler_acquisition_artifacts
  ADD COLUMN provider_archive_entry TEXT NOT NULL DEFAULT '';
ALTER TABLE filler_acquisition_artifacts
  ADD COLUMN provider_archive_committed INTEGER NOT NULL DEFAULT 0
  CHECK (provider_archive_committed IN (0, 1));

-- Forward-only (§16).

-- +goose Down
SELECT 1;
