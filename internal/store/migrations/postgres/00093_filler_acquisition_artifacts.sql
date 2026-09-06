-- +goose Up
-- V65 (§10). Postgres mirror of sqlite 00091; the full reasoning lives there.

CREATE TABLE IF NOT EXISTS filler_acquisition_artifacts (
  id             TEXT PRIMARY KEY,
  acquisition_id TEXT NOT NULL REFERENCES filler_acquisition_runs(id),
  source_id      TEXT NOT NULL DEFAULT '',
  provider       TEXT NOT NULL,
  source_url     TEXT NOT NULL,
  staging_path   TEXT NOT NULL DEFAULT '',
  media_path     TEXT NOT NULL,
  sidecar_path   TEXT NOT NULL DEFAULT '',
  media_sha256   TEXT NOT NULL,
  media_bytes    BIGINT NOT NULL,
  clip_hash      TEXT NOT NULL DEFAULT '',
  state          TEXT NOT NULL,
  repair_reason  TEXT NOT NULL DEFAULT '',
  completed_at   BIGINT NOT NULL,
  updated_at     BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_filler_acquisition_artifacts_path
  ON filler_acquisition_artifacts(media_path, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_filler_acquisition_artifacts_clip
  ON filler_acquisition_artifacts(clip_hash) WHERE clip_hash <> '';
CREATE INDEX IF NOT EXISTS idx_filler_acquisition_artifacts_recovery
  ON filler_acquisition_artifacts(state, updated_at);

-- Forward-only (§16).

-- +goose Down
SELECT 1;
