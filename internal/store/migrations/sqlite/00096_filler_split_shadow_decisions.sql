-- +goose Up
-- V67 (§10): immutable compatibility-versus-complete-plan split decisions. The proposal may be
-- consumed after publication, so shadow evidence has its own append-only lifetime.

CREATE TABLE IF NOT EXISTS filler_split_shadow_decisions (
  id                 TEXT PRIMARY KEY,
  proposal_id        TEXT NOT NULL,
  clip_hash          TEXT NOT NULL,
  source_sha256      TEXT NOT NULL DEFAULT '',
  assessment_sha256  TEXT NOT NULL DEFAULT '',
  policy_version     TEXT NOT NULL,
  decision_json      TEXT NOT NULL,
  observed_at        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_filler_split_shadow_decisions_clip
  ON filler_split_shadow_decisions(clip_hash, observed_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_filler_split_shadow_decisions_proposal
  ON filler_split_shadow_decisions(proposal_id, observed_at DESC, id DESC);

-- Forward-only (§16).

-- +goose Down
SELECT 1;
