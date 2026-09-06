-- +goose Up
-- V66 (§10): candidate-level pulls retain the provider item identity after download.

ALTER TABLE filler_acquisition_artifacts ADD COLUMN remote_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_filler_acquisition_artifacts_remote
  ON filler_acquisition_artifacts(provider, source_id, remote_id)
  WHERE remote_id <> '';

-- Forward-only (§16).

-- +goose Down
SELECT 1;
