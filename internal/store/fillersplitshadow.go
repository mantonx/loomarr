package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/filler"
)

const structureSplitShadowSelect = `SELECT id, proposal_id, clip_hash, source_sha256,
       assessment_sha256, policy_version, decision_json, observed_at
  FROM filler_split_shadow_decisions`

func (s *sqlStore) PutStructureSplitShadowDecision(ctx context.Context, decision filler.StructureSplitShadowDecision) error {
	if err := filler.ValidateStructureSplitShadowDecision(decision); err != nil {
		return err
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode structure split shadow decision: %w", err)
	}
	result, err := s.db.ExecContext(ctx, s.ph(`INSERT INTO filler_split_shadow_decisions
        (id, proposal_id, clip_hash, source_sha256, assessment_sha256, policy_version, decision_json, observed_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO NOTHING`),
		decision.ID, decision.ProposalID, decision.ClipHash, decision.SourceSHA256,
		decision.AssessmentSHA256, decision.PolicyVersion, string(raw), epoch(decision.ObservedAt))
	if err != nil {
		return fmt.Errorf("insert structure split shadow decision %s: %w", decision.ID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect structure split shadow decision %s: %w", decision.ID, err)
	}
	if n == 1 {
		return nil
	}
	var existing string
	if err := s.db.QueryRowContext(ctx, s.ph(`SELECT decision_json FROM filler_split_shadow_decisions WHERE id = ?`), decision.ID).Scan(&existing); err != nil {
		return fmt.Errorf("read existing structure split shadow decision %s: %w", decision.ID, err)
	}
	if !bytes.Equal([]byte(existing), raw) {
		return filler.ErrStructureSplitShadowConflict
	}
	return nil
}

func (s *sqlStore) GetStructureSplitShadowDecision(ctx context.Context, id string) (filler.StructureSplitShadowDecision, bool, error) {
	if id == "" {
		return filler.StructureSplitShadowDecision{}, false, fmt.Errorf("get structure split shadow decision: id is required")
	}
	decision, err := scanStructureSplitShadowDecision(s.db.QueryRowContext(ctx, s.ph(structureSplitShadowSelect+` WHERE id = ?`), id))
	if errors.Is(err, ErrNotFound) {
		return filler.StructureSplitShadowDecision{}, false, nil
	}
	if err != nil {
		return filler.StructureSplitShadowDecision{}, false, fmt.Errorf("get structure split shadow decision %s: %w", id, err)
	}
	return decision, true, nil
}

func (s *sqlStore) ListStructureSplitShadowDecisions(ctx context.Context, clipHash string, limit int) ([]filler.StructureSplitShadowDecision, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("list structure split shadow decisions: limit must be between 1 and 1000")
	}
	query := structureSplitShadowSelect
	var args []any
	if clipHash != "" {
		query += ` WHERE clip_hash = ?`
		args = append(args, clipHash)
	}
	query += ` ORDER BY observed_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, s.ph(query), args...)
	if err != nil {
		return nil, fmt.Errorf("list structure split shadow decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	decisions := make([]filler.StructureSplitShadowDecision, 0)
	for rows.Next() {
		decision, scanErr := scanStructureSplitShadowDecision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		decisions = append(decisions, decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list structure split shadow decisions: %w", err)
	}
	return decisions, nil
}

func scanStructureSplitShadowDecision(scanner scannable) (filler.StructureSplitShadowDecision, error) {
	var id, proposalID, clipHash, sourceSHA, assessmentSHA, policyVersion, raw string
	var observedAt int64
	if err := scanner.Scan(&id, &proposalID, &clipHash, &sourceSHA, &assessmentSHA, &policyVersion, &raw, &observedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return filler.StructureSplitShadowDecision{}, ErrNotFound
		}
		return filler.StructureSplitShadowDecision{}, err
	}
	var decision filler.StructureSplitShadowDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return filler.StructureSplitShadowDecision{}, fmt.Errorf("decode structure split shadow decision %s: %w", id, err)
	}
	if decision.ID != id || decision.ProposalID != proposalID || decision.ClipHash != clipHash || decision.SourceSHA256 != sourceSHA || decision.AssessmentSHA256 != assessmentSHA || decision.PolicyVersion != policyVersion || epoch(decision.ObservedAt) != observedAt {
		return filler.StructureSplitShadowDecision{}, fmt.Errorf("structure split shadow decision %s columns drift from its document", id)
	}
	if err := filler.ValidateStructureSplitShadowDecision(decision); err != nil {
		return filler.StructureSplitShadowDecision{}, fmt.Errorf("validate structure split shadow decision %s: %w", id, err)
	}
	return decision, nil
}
