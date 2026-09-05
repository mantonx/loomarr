package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/inventory"
)

func (s *sqlStore) ApplyInventorySnapshot(ctx context.Context, snapshot inventory.Snapshot) (inventory.ItemID, error) {
	clean, err := inventory.ValidateSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin inventory snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	itemID, exact, err := s.inventoryItemIDByOrigin(ctx, tx, clean.Origin)
	if err != nil {
		return "", err
	}
	grounded, err := s.inventoryItemIDsByExternalIDs(ctx, tx, clean.ExternalIDs)
	if err != nil {
		return "", err
	}
	if len(grounded) > 1 || (exact && len(grounded) == 1 && !grounded[itemID]) {
		return "", inventory.ErrIdentityConflict
	}
	if !exact {
		for id := range grounded {
			itemID = id
		}
		if itemID == "" {
			itemID = inventory.ItemID(stableInventoryID("item", string(clean.Origin.Authority), clean.Origin.ExternalItemID))
		}
	}

	at := clean.Observation.ObservedAt
	if _, err := tx.ExecContext(ctx, s.ph(`
		INSERT INTO inventory_items (id, kind, created_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind=excluded.kind, updated_at=excluded.updated_at`),
		string(itemID), string(clean.Kind), epoch(at), epoch(at)); err != nil {
		return "", fmt.Errorf("upsert inventory item: %w", err)
	}
	observationJSON, err := json.Marshal(clean.Observation)
	if err != nil {
		return "", fmt.Errorf("marshal inventory item observation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.ph(`
		INSERT INTO inventory_item_origins
		  (authority_id, external_item_id, item_id, observation_json, observed_at, last_seen_at, missing_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(authority_id, external_item_id) DO UPDATE SET
		  item_id=excluded.item_id, observation_json=excluded.observation_json,
		  observed_at=excluded.observed_at, last_seen_at=excluded.last_seen_at, missing_at=NULL`),
		string(clean.Origin.Authority), clean.Origin.ExternalItemID, string(itemID), string(observationJSON),
		epoch(at), epoch(at)); err != nil {
		return "", fmt.Errorf("upsert inventory item origin: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.ph(`DELETE FROM inventory_external_ids
		WHERE authority_id = ? AND external_item_id = ?`), string(clean.Origin.Authority), clean.Origin.ExternalItemID); err != nil {
		return "", fmt.Errorf("replace inventory external ids: %w", err)
	}
	for _, externalID := range clean.ExternalIDs {
		if _, err := tx.ExecContext(ctx, s.ph(`
			INSERT INTO inventory_external_ids
			  (item_id, authority_id, external_item_id, namespace, external_id)
			VALUES (?, ?, ?, ?, ?)`), string(itemID), string(clean.Origin.Authority), clean.Origin.ExternalItemID,
			externalID.Namespace, externalID.Value); err != nil {
			return "", fmt.Errorf("insert inventory external id: %w", err)
		}
	}
	for _, source := range clean.Sources {
		if err := s.applyInventorySource(ctx, tx, itemID, clean.Origin, source); err != nil {
			return "", err
		}
	}
	if clean.Observation.Coverage["sources"] == inventory.CoverageEmpty {
		if _, err := tx.ExecContext(ctx, s.ph(`UPDATE inventory_source_origins SET missing_at = ?
			WHERE authority_id = ? AND external_item_id = ?`), epoch(at), string(clean.Origin.Authority), clean.Origin.ExternalItemID); err != nil {
			return "", fmt.Errorf("mark empty inventory sources: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit inventory snapshot: %w", err)
	}
	return itemID, nil
}

func (s *sqlStore) applyInventorySource(
	ctx context.Context,
	tx *sql.Tx,
	itemID inventory.ItemID,
	origin inventory.OriginKey,
	source inventory.SourceSnapshot,
) error {
	var sourceID inventory.SourceID
	err := tx.QueryRowContext(ctx, s.ph(`SELECT source_id FROM inventory_source_origins
		WHERE authority_id = ? AND external_item_id = ? AND external_source_id = ?`),
		string(origin.Authority), origin.ExternalItemID, source.ExternalSourceID).Scan(&sourceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find inventory source origin: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		sourceID = inventory.SourceID(stableInventoryID(
			"source", string(itemID), string(origin.Authority), origin.ExternalItemID, source.ExternalSourceID,
		))
	}
	var previousRevision string
	err = tx.QueryRowContext(ctx, s.ph(`SELECT revision FROM inventory_sources WHERE id = ?`), string(sourceID)).Scan(&previousRevision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read inventory source revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.ph(`
		INSERT INTO inventory_sources (id, item_id, kind, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET item_id=excluded.item_id, kind=excluded.kind,
		  revision=excluded.revision, updated_at=excluded.updated_at`), string(sourceID), string(itemID),
		string(source.Kind), source.Revision, epoch(source.Observation.ObservedAt), epoch(source.Observation.ObservedAt)); err != nil {
		return fmt.Errorf("upsert inventory source: %w", err)
	}
	if previousRevision != "" && previousRevision != source.Revision {
		if _, err := tx.ExecContext(ctx, s.ph(`DELETE FROM inventory_source_measurements WHERE source_id = ?`), string(sourceID)); err != nil {
			return fmt.Errorf("invalidate inventory source measurement: %w", err)
		}
	}
	locatorJSON, err := json.Marshal(source.Locator)
	if err != nil {
		return fmt.Errorf("marshal inventory source locator: %w", err)
	}
	observationJSON, err := json.Marshal(source.Observation)
	if err != nil {
		return fmt.Errorf("marshal inventory source observation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.ph(`
		INSERT INTO inventory_source_origins
		  (authority_id, external_item_id, external_source_id, source_id, locator_json,
		   observation_json, observed_at, last_seen_at, missing_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(authority_id, external_item_id, external_source_id) DO UPDATE SET
		  source_id=excluded.source_id, locator_json=excluded.locator_json,
		  observation_json=excluded.observation_json, observed_at=excluded.observed_at,
		  last_seen_at=excluded.last_seen_at, missing_at=NULL`), string(origin.Authority), origin.ExternalItemID,
		source.ExternalSourceID, string(sourceID), string(locatorJSON), string(observationJSON),
		epoch(source.Observation.ObservedAt), epoch(source.Observation.ObservedAt)); err != nil {
		return fmt.Errorf("upsert inventory source origin: %w", err)
	}
	return nil
}

func (s *sqlStore) inventoryItemIDByOrigin(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	origin inventory.OriginKey,
) (inventory.ItemID, bool, error) {
	var itemID inventory.ItemID
	err := queryer.QueryRowContext(ctx, s.ph(`SELECT item_id FROM inventory_item_origins
		WHERE authority_id = ? AND external_item_id = ?`), string(origin.Authority), origin.ExternalItemID).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find inventory item origin: %w", err)
	}
	return itemID, true, nil
}

func (s *sqlStore) inventoryItemIDsByExternalIDs(
	ctx context.Context,
	tx *sql.Tx,
	externalIDs []inventory.ExternalID,
) (map[inventory.ItemID]bool, error) {
	ids := make(map[inventory.ItemID]bool)
	for _, externalID := range externalIDs {
		rows, err := tx.QueryContext(ctx, s.ph(`SELECT DISTINCT item_id FROM inventory_external_ids
			WHERE namespace = ? AND external_id = ?`), externalID.Namespace, externalID.Value)
		if err != nil {
			return nil, fmt.Errorf("find grounded inventory identity: %w", err)
		}
		for rows.Next() {
			var id inventory.ItemID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan grounded inventory identity: %w", err)
			}
			ids[id] = true
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close grounded inventory identity rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read grounded inventory identity rows: %w", err)
		}
	}
	return ids, nil
}

func (s *sqlStore) InventoryItem(ctx context.Context, ref inventory.ItemRef) (inventory.Item, bool, error) {
	if err := inventory.ValidateItemRef(ref); err != nil {
		return inventory.Item{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return inventory.Item{}, false, fmt.Errorf("begin inventory read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	itemID := ref.ID
	if ref.Origin != nil {
		var ok bool
		itemID, ok, err = s.inventoryItemIDByOrigin(ctx, tx, *ref.Origin)
		if err != nil || !ok {
			return inventory.Item{}, false, err
		}
	}
	item, ok, err := s.readInventoryItem(ctx, tx, itemID)
	if err != nil || !ok {
		return inventory.Item{}, ok, err
	}
	if err := tx.Commit(); err != nil {
		return inventory.Item{}, false, fmt.Errorf("commit inventory read: %w", err)
	}
	item, err = inventory.ValidateItem(item)
	if err != nil {
		return inventory.Item{}, false, fmt.Errorf("validate stored inventory item: %w", err)
	}
	return item, true, nil
}

func (s *sqlStore) readInventoryItem(ctx context.Context, tx *sql.Tx, itemID inventory.ItemID) (inventory.Item, bool, error) {
	var item inventory.Item
	err := tx.QueryRowContext(ctx, s.ph(`SELECT id, kind FROM inventory_items WHERE id = ?`), string(itemID)).Scan(&item.ID, &item.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return inventory.Item{}, false, nil
	}
	if err != nil {
		return inventory.Item{}, false, fmt.Errorf("read inventory item: %w", err)
	}
	originRows, err := tx.QueryContext(ctx, s.ph(`SELECT authority_id, external_item_id,
		observation_json, last_seen_at, COALESCE(missing_at, 0)
		FROM inventory_item_origins WHERE item_id = ? ORDER BY authority_id, external_item_id`), string(itemID))
	if err != nil {
		return inventory.Item{}, false, fmt.Errorf("read inventory item origins: %w", err)
	}
	for originRows.Next() {
		var origin inventory.ItemOrigin
		var observationJSON string
		var lastSeenAt, missingAt int64
		if err := originRows.Scan(&origin.Key.Authority, &origin.Key.ExternalItemID, &observationJSON, &lastSeenAt, &missingAt); err != nil {
			_ = originRows.Close()
			return inventory.Item{}, false, fmt.Errorf("scan inventory item origin: %w", err)
		}
		if err := json.Unmarshal([]byte(observationJSON), &origin.Observation); err != nil {
			_ = originRows.Close()
			return inventory.Item{}, false, fmt.Errorf("decode inventory item observation: %w", err)
		}
		origin.LastSeenAt, origin.MissingAt = fromEpoch(lastSeenAt), fromEpoch(missingAt)
		item.Origins = append(item.Origins, origin)
	}
	if err := originRows.Close(); err != nil {
		return inventory.Item{}, false, fmt.Errorf("close inventory item origins: %w", err)
	}
	if err := originRows.Err(); err != nil {
		return inventory.Item{}, false, fmt.Errorf("scan inventory item origins: %w", err)
	}
	externalRows, err := tx.QueryContext(ctx, s.ph(`SELECT DISTINCT namespace, external_id
		FROM inventory_external_ids WHERE item_id = ? ORDER BY namespace, external_id`), string(itemID))
	if err != nil {
		return inventory.Item{}, false, fmt.Errorf("read inventory external ids: %w", err)
	}
	for externalRows.Next() {
		var externalID inventory.ExternalID
		if err := externalRows.Scan(&externalID.Namespace, &externalID.Value); err != nil {
			_ = externalRows.Close()
			return inventory.Item{}, false, fmt.Errorf("scan inventory external id: %w", err)
		}
		item.ExternalIDs = append(item.ExternalIDs, externalID)
	}
	if err := externalRows.Close(); err != nil {
		return inventory.Item{}, false, fmt.Errorf("close inventory external ids: %w", err)
	}
	if err := externalRows.Err(); err != nil {
		return inventory.Item{}, false, fmt.Errorf("scan inventory external ids: %w", err)
	}
	sourceRows, err := tx.QueryContext(ctx, s.ph(`SELECT id, kind, revision FROM inventory_sources
		WHERE item_id = ? ORDER BY id`), string(itemID))
	if err != nil {
		return inventory.Item{}, false, fmt.Errorf("read inventory sources: %w", err)
	}
	for sourceRows.Next() {
		var source inventory.Source
		source.ItemID = item.ID
		if err := sourceRows.Scan(&source.ID, &source.Kind, &source.Revision); err != nil {
			_ = sourceRows.Close()
			return inventory.Item{}, false, fmt.Errorf("scan inventory source: %w", err)
		}
		item.Sources = append(item.Sources, source)
	}
	if err := sourceRows.Close(); err != nil {
		return inventory.Item{}, false, fmt.Errorf("close inventory sources: %w", err)
	}
	if err := sourceRows.Err(); err != nil {
		return inventory.Item{}, false, fmt.Errorf("scan inventory sources: %w", err)
	}
	for i := range item.Sources {
		if err := s.readInventorySourceDetails(ctx, tx, &item.Sources[i]); err != nil {
			return inventory.Item{}, false, err
		}
	}
	return item, true, nil
}

func (s *sqlStore) readInventorySourceDetails(ctx context.Context, tx *sql.Tx, source *inventory.Source) error {
	rows, err := tx.QueryContext(ctx, s.ph(`SELECT authority_id, external_item_id, external_source_id,
		locator_json, observation_json, last_seen_at, COALESCE(missing_at, 0)
		FROM inventory_source_origins WHERE source_id = ? ORDER BY authority_id, external_item_id, external_source_id`),
		string(source.ID))
	if err != nil {
		return fmt.Errorf("read inventory source origins: %w", err)
	}
	for rows.Next() {
		var origin inventory.SourceOrigin
		var locatorJSON, observationJSON string
		var lastSeenAt, missingAt int64
		if err := rows.Scan(&origin.Key.Authority, &origin.Key.ExternalItemID, &origin.ExternalSourceID,
			&locatorJSON, &observationJSON, &lastSeenAt, &missingAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan inventory source origin: %w", err)
		}
		if err := json.Unmarshal([]byte(locatorJSON), &origin.Locator); err != nil {
			_ = rows.Close()
			return fmt.Errorf("decode inventory source locator: %w", err)
		}
		if err := json.Unmarshal([]byte(observationJSON), &origin.Observation); err != nil {
			_ = rows.Close()
			return fmt.Errorf("decode inventory source observation: %w", err)
		}
		origin.LastSeenAt, origin.MissingAt = fromEpoch(lastSeenAt), fromEpoch(missingAt)
		source.Origins = append(source.Origins, origin)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close inventory source origins: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan inventory source origins: %w", err)
	}
	var measurement inventory.Measurement
	var observationJSON string
	err = tx.QueryRowContext(ctx, s.ph(`SELECT source_revision, observation_json
		FROM inventory_source_measurements WHERE source_id = ?`), string(source.ID)).Scan(&measurement.Revision, &observationJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read inventory source measurement: %w", err)
	}
	measurement.SourceID = source.ID
	if err := json.Unmarshal([]byte(observationJSON), &measurement.Observation); err != nil {
		return fmt.Errorf("decode inventory source measurement: %w", err)
	}
	source.Measurement = &measurement
	return nil
}

func (s *sqlStore) RecordInventoryMeasurement(ctx context.Context, measurement inventory.Measurement) error {
	clean, err := inventory.ValidateMeasurement(measurement)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin inventory measurement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var revision string
	err = tx.QueryRowContext(ctx, s.ph(`SELECT revision FROM inventory_sources WHERE id = ?`), string(clean.SourceID)).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read measured inventory source: %w", err)
	}
	if revision != clean.Revision {
		return inventory.ErrSourceRevisionGone
	}
	observationJSON, err := json.Marshal(clean.Observation)
	if err != nil {
		return fmt.Errorf("marshal inventory measurement: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.ph(`
		INSERT INTO inventory_source_measurements (source_id, source_revision, observation_json, observed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source_id) DO UPDATE SET source_revision=excluded.source_revision,
		  observation_json=excluded.observation_json, observed_at=excluded.observed_at`), string(clean.SourceID),
		clean.Revision, string(observationJSON), epoch(clean.Observation.ObservedAt)); err != nil {
		return fmt.Errorf("upsert inventory measurement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit inventory measurement: %w", err)
	}
	return nil
}

func (s *sqlStore) MarkInventoryUnseen(
	ctx context.Context,
	authority inventory.AuthorityID,
	at time.Time,
	seen []inventory.OriginKey,
) error {
	authority = inventory.AuthorityID(strings.TrimSpace(string(authority)))
	if authority == "" || at.IsZero() {
		return inventory.ErrInvalid
	}
	seenIDs := make(map[string]bool, len(seen))
	for _, key := range seen {
		if key.Authority != authority || strings.TrimSpace(key.ExternalItemID) == "" {
			return inventory.ErrInvalid
		}
		seenIDs[key.ExternalItemID] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin inventory missing reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, s.ph(`SELECT external_item_id FROM inventory_item_origins
		WHERE authority_id = ?`), string(authority))
	if err != nil {
		return fmt.Errorf("read inventory authority origins: %w", err)
	}
	var missing []string
	for rows.Next() {
		var externalItemID string
		if err := rows.Scan(&externalItemID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan inventory authority origin: %w", err)
		}
		if !seenIDs[externalItemID] {
			missing = append(missing, externalItemID)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close inventory authority origins: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan inventory authority origins: %w", err)
	}
	for _, externalItemID := range missing {
		if _, err := tx.ExecContext(ctx, s.ph(`UPDATE inventory_item_origins SET missing_at = ?
			WHERE authority_id = ? AND external_item_id = ?`), epoch(at), string(authority), externalItemID); err != nil {
			return fmt.Errorf("mark inventory item origin missing: %w", err)
		}
		if _, err := tx.ExecContext(ctx, s.ph(`UPDATE inventory_source_origins SET missing_at = ?
			WHERE authority_id = ? AND external_item_id = ?`), epoch(at), string(authority), externalItemID); err != nil {
			return fmt.Errorf("mark inventory source origin missing: %w", err)
		}
	}
	return tx.Commit()
}

func stableInventoryID(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}
