-- +goose Up
-- V67 (§10). Postgres mirror of sqlite 00096; the full reasoning lives there.

CREATE TABLE IF NOT EXISTS filler_rights_grants (
  grant_sha256         TEXT PRIMARY KEY,
  source_id            TEXT NOT NULL,
  acquisition_id       TEXT NOT NULL,
  source_master_sha256 TEXT NOT NULL,
  policy_sha256        TEXT NOT NULL,
  use_name             TEXT NOT NULL,
  supersedes_sha256    TEXT,
  grant_json           TEXT NOT NULL,
  recorded_at          BIGINT NOT NULL,
  FOREIGN KEY (supersedes_sha256) REFERENCES filler_rights_grants(grant_sha256)
);

CREATE INDEX IF NOT EXISTS idx_filler_rights_grants_scope
  ON filler_rights_grants(source_id, acquisition_id, source_master_sha256, policy_sha256, use_name, recorded_at);

CREATE TABLE IF NOT EXISTS filler_rights_heads (
  source_id            TEXT NOT NULL,
  acquisition_id       TEXT NOT NULL,
  source_master_sha256 TEXT NOT NULL,
  policy_sha256        TEXT NOT NULL,
  use_name             TEXT NOT NULL,
  grant_sha256         TEXT NOT NULL REFERENCES filler_rights_grants(grant_sha256),
  PRIMARY KEY (source_id, acquisition_id, source_master_sha256, policy_sha256, use_name)
);

-- Forward-only (§16).

-- +goose Down
SELECT 1;
