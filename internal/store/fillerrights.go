package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/filler"
)

const fillerRightsScopeWhere = `source_id = ? AND acquisition_id = ? AND source_master_sha256 = ? AND policy_sha256 = ? AND use_name = ?`
const fillerRightsHeadScopeWhere = `h.source_id = ? AND h.acquisition_id = ? AND h.source_master_sha256 = ? AND h.policy_sha256 = ? AND h.use_name = ?`

func (s *sqlStore) PutFillerRightsGrant(ctx context.Context, grant filler.FillerRightsGrant) error {
	if err := filler.ValidateFillerRightsGrant(grant); err != nil {
		return err
	}
	payload, err := json.Marshal(grant)
	if err != nil {
		return fmt.Errorf("encode filler rights grant: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin filler rights grant: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, found, err := currentFillerRightsHead(ctx, tx, s.ph, grant.Scope)
	if err != nil {
		return err
	}
	if found && current == grant.SHA256 {
		var existing string
		if err := tx.QueryRowContext(ctx, s.ph(`SELECT grant_json FROM filler_rights_grants WHERE grant_sha256 = ?`), grant.SHA256).Scan(&existing); err != nil {
			return fmt.Errorf("read existing filler rights grant: %w", err)
		}
		if existing != string(payload) {
			return filler.ErrFillerRightsGrantConflict
		}
		return nil
	}
	if found && grant.SupersedesSHA256 != current || !found && grant.SupersedesSHA256 != "" {
		return filler.ErrFillerRightsGrantConflict
	}

	var supersedes any
	if grant.SupersedesSHA256 != "" {
		supersedes = grant.SupersedesSHA256
	}
	result, err := tx.ExecContext(ctx, s.ph(`INSERT INTO filler_rights_grants (
		grant_sha256, source_id, acquisition_id, source_master_sha256, policy_sha256,
		use_name, supersedes_sha256, grant_json, recorded_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(grant_sha256) DO NOTHING`),
		grant.SHA256, grant.Scope.SourceID, grant.Scope.AcquisitionID, grant.Scope.SourceMasterSHA256,
		grant.Scope.PolicySHA256, grant.Scope.Use, supersedes, string(payload), grant.RecordedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert filler rights grant: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect filler rights grant insert: %w", err)
	}
	if inserted == 0 {
		var existing string
		if err := tx.QueryRowContext(ctx, s.ph(`SELECT grant_json FROM filler_rights_grants WHERE grant_sha256 = ?`), grant.SHA256).Scan(&existing); err != nil {
			return fmt.Errorf("read colliding filler rights grant: %w", err)
		}
		if existing != string(payload) {
			return filler.ErrFillerRightsGrantConflict
		}
	}

	if found {
		result, err = tx.ExecContext(ctx, s.ph(`UPDATE filler_rights_heads SET grant_sha256 = ? WHERE `+fillerRightsScopeWhere+` AND grant_sha256 = ?`),
			grant.SHA256, grant.Scope.SourceID, grant.Scope.AcquisitionID, grant.Scope.SourceMasterSHA256,
			grant.Scope.PolicySHA256, grant.Scope.Use, current)
	} else {
		result, err = tx.ExecContext(ctx, s.ph(`INSERT INTO filler_rights_heads (
			source_id, acquisition_id, source_master_sha256, policy_sha256, use_name, grant_sha256
		) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(source_id, acquisition_id, source_master_sha256, policy_sha256, use_name) DO NOTHING`),
			grant.Scope.SourceID, grant.Scope.AcquisitionID, grant.Scope.SourceMasterSHA256,
			grant.Scope.PolicySHA256, grant.Scope.Use, grant.SHA256)
	}
	if err != nil {
		return fmt.Errorf("advance filler rights authority: %w", err)
	}
	advanced, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect filler rights authority advance: %w", err)
	}
	if advanced != 1 {
		return filler.ErrFillerRightsGrantConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit filler rights grant: %w", err)
	}
	return nil
}

func (s *sqlStore) CurrentFillerRightsGrant(ctx context.Context, scope filler.FillerRightsScope) (filler.FillerRightsGrant, bool, error) {
	if err := filler.ValidateFillerRightsScope(scope); err != nil {
		return filler.FillerRightsGrant{}, false, err
	}
	var payload string
	var storedSHA256, sourceID, acquisitionID, sourceMasterSHA256, policySHA256, useName string
	err := s.db.QueryRowContext(ctx, s.ph(`SELECT g.grant_json, g.grant_sha256, g.source_id,
		g.acquisition_id, g.source_master_sha256, g.policy_sha256, g.use_name
		FROM filler_rights_heads h JOIN filler_rights_grants g ON g.grant_sha256 = h.grant_sha256
		WHERE `+fillerRightsHeadScopeWhere),
		scope.SourceID, scope.AcquisitionID, scope.SourceMasterSHA256, scope.PolicySHA256, scope.Use,
	).Scan(&payload, &storedSHA256, &sourceID, &acquisitionID, &sourceMasterSHA256, &policySHA256, &useName)
	if errors.Is(err, sql.ErrNoRows) {
		return filler.FillerRightsGrant{}, false, nil
	}
	if err != nil {
		return filler.FillerRightsGrant{}, false, fmt.Errorf("read current filler rights grant: %w", err)
	}
	var grant filler.FillerRightsGrant
	if err := json.Unmarshal([]byte(payload), &grant); err != nil {
		return filler.FillerRightsGrant{}, false, fmt.Errorf("decode current filler rights grant: %w", err)
	}
	storedScope := filler.FillerRightsScope{
		SourceID: sourceID, AcquisitionID: acquisitionID, SourceMasterSHA256: sourceMasterSHA256,
		PolicySHA256: policySHA256, Use: useName,
	}
	if filler.ValidateFillerRightsGrant(grant) != nil || grant.SHA256 != storedSHA256 || grant.Scope != storedScope || storedScope != scope {
		return filler.FillerRightsGrant{}, false, fmt.Errorf("current filler rights grant storage is inconsistent")
	}
	return grant, true, nil
}

func currentFillerRightsHead(ctx context.Context, tx *sql.Tx, ph placeholder, scope filler.FillerRightsScope) (string, bool, error) {
	var current string
	err := tx.QueryRowContext(ctx, ph(`SELECT grant_sha256 FROM filler_rights_heads WHERE `+fillerRightsScopeWhere),
		scope.SourceID, scope.AcquisitionID, scope.SourceMasterSHA256, scope.PolicySHA256, scope.Use,
	).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read current filler rights authority: %w", err)
	}
	return current, true, nil
}

var _ filler.FillerRightsGrantRepository = (*sqlStore)(nil)
