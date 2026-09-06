-- +goose Up
-- V67 (§10). Postgres mirror of sqlite 00095; the full reasoning lives there.

CREATE TABLE IF NOT EXISTS filler_structure_window_call_ledger (
  request_sha256      TEXT PRIMARY KEY,
  evaluation_id       TEXT NOT NULL UNIQUE REFERENCES filler_inference_evaluations(id),
  source_sha256       TEXT NOT NULL,
  media_set_sha256    TEXT NOT NULL,
  window_ordinal      INTEGER NOT NULL,
  assessor_id         TEXT NOT NULL,
  state               TEXT NOT NULL,
  reservation_json    TEXT NOT NULL,
  record_sha256       TEXT NOT NULL DEFAULT '',
  record_json         TEXT NOT NULL DEFAULT '',
  requested_at        BIGINT NOT NULL,
  assessed_at         BIGINT NOT NULL DEFAULT 0,
  CHECK (window_ordinal >= 0),
  CHECK (state IN ('open', 'held_budget', 'settled')),
  CHECK (
    (state = 'settled' AND record_sha256 <> '' AND record_json <> '' AND assessed_at > 0) OR
    (state <> 'settled' AND record_sha256 = '' AND record_json = '' AND assessed_at = 0)
  )
);

CREATE INDEX IF NOT EXISTS idx_filler_structure_window_call_source
  ON filler_structure_window_call_ledger(source_sha256, media_set_sha256, requested_at DESC, request_sha256);
CREATE INDEX IF NOT EXISTS idx_filler_structure_window_call_open
  ON filler_structure_window_call_ledger(state, requested_at ASC, request_sha256);

-- Forward-only (§16).

-- +goose Down
SELECT 1;
