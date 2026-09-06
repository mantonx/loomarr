-- +goose Up
-- V65 (§10). Postgres mirror of SQLite 00094.

ALTER TABLE filler_acquisition_artifacts
  ADD COLUMN provider_archive_entry TEXT NOT NULL DEFAULT '';
ALTER TABLE filler_acquisition_artifacts
  ADD COLUMN provider_archive_committed BIGINT NOT NULL DEFAULT 0
  CHECK (provider_archive_committed IN (0, 1));

-- Forward-only (§16).

-- +goose Down
SELECT 1;
