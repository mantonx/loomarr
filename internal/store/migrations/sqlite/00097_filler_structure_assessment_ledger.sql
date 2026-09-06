-- +goose Up
-- V67 (§10): durable complete-timeline reservations and content-addressed settlements.
-- The referenced inference row owns shared spend accounting; this journal owns structure-specific
-- request and assessment authority. JSON is TEXT in both dialects so exact bytes remain inspectable.

CREATE TABLE IF NOT EXISTS filler_structure_assessment_ledger (
  request_sha256      TEXT PRIMARY KEY,
  evaluation_id       TEXT NOT NULL UNIQUE REFERENCES filler_inference_evaluations(id),
  source_sha256       TEXT NOT NULL,
  assessor_id         TEXT NOT NULL,
  state               TEXT NOT NULL,
  reservation_json    TEXT NOT NULL,
  assessment_sha256   TEXT NOT NULL DEFAULT '',
  record_json         TEXT NOT NULL DEFAULT '',
  requested_at        INTEGER NOT NULL,
  assessed_at         INTEGER NOT NULL DEFAULT 0,
  CHECK (state IN ('open', 'held_budget', 'settled')),
  CHECK (
    (state = 'settled' AND assessment_sha256 <> '' AND record_json <> '' AND assessed_at > 0) OR
    (state <> 'settled' AND assessment_sha256 = '' AND record_json = '' AND assessed_at = 0)
  )
);

CREATE INDEX IF NOT EXISTS idx_filler_structure_assessment_source
  ON filler_structure_assessment_ledger(source_sha256, requested_at DESC, request_sha256);
CREATE INDEX IF NOT EXISTS idx_filler_structure_assessment_open
  ON filler_structure_assessment_ledger(state, requested_at ASC, request_sha256);

-- Forward-only (§16).

-- +goose Down
SELECT 1;
