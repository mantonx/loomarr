-- +goose Up
-- §8.1: path-free spoken-safety run identity plus append-only execution events.
-- Payload JSON is TEXT in both dialects so exact bytes remain the idempotency authority.

CREATE TABLE IF NOT EXISTS filler_spoken_safety_runs (
  id                    TEXT PRIMARY KEY,
  clip_hash             TEXT NOT NULL,
  authority_sha256      TEXT NOT NULL,
  source_sha256         TEXT NOT NULL,
  source_bytes          INTEGER NOT NULL,
  duration_ms           INTEGER NOT NULL,
  certification_sha256  TEXT NOT NULL,
  policy_sha256         TEXT NOT NULL,
  proposer_sha256       TEXT NOT NULL,
  implementation        TEXT NOT NULL,
  created_at            INTEGER NOT NULL,
  CHECK (source_bytes > 0 AND duration_ms > 0)
);

CREATE INDEX IF NOT EXISTS idx_filler_spoken_safety_runs_clip
  ON filler_spoken_safety_runs(clip_hash, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS filler_spoken_safety_events (
  id             TEXT PRIMARY KEY,
  run_id         TEXT NOT NULL REFERENCES filler_spoken_safety_runs(id),
  ordinal        INTEGER NOT NULL,
  kind           TEXT NOT NULL,
  inference_id   TEXT REFERENCES filler_inference_evaluations(id),
  payload_json   TEXT NOT NULL,
  created_at     INTEGER NOT NULL,
  UNIQUE (run_id, ordinal),
  CHECK (ordinal >= 0),
  CHECK (kind IN ('source_planned', 'proposal_completed', 'inference_reserved', 'inference_settled', 'terminal')),
  CHECK (
    (kind IN ('inference_reserved', 'inference_settled') AND inference_id IS NOT NULL) OR
    (kind NOT IN ('inference_reserved', 'inference_settled') AND inference_id IS NULL)
  )
);

CREATE INDEX IF NOT EXISTS idx_filler_spoken_safety_events_run
  ON filler_spoken_safety_events(run_id, ordinal ASC);
CREATE INDEX IF NOT EXISTS idx_filler_spoken_safety_events_inference
  ON filler_spoken_safety_events(inference_id) WHERE inference_id IS NOT NULL;

-- Forward-only (§16).

-- +goose Down
SELECT 1;
