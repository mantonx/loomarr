-- +goose Up
CREATE TABLE inventory_items (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE TABLE inventory_item_origins (
    authority_id TEXT NOT NULL,
    external_item_id TEXT NOT NULL,
    item_id TEXT NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
    observation_json TEXT NOT NULL,
    observed_at BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    missing_at BIGINT,
    PRIMARY KEY (authority_id, external_item_id)
);
CREATE INDEX idx_inventory_item_origins_item ON inventory_item_origins (item_id);

CREATE TABLE inventory_external_ids (
    item_id TEXT NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
    authority_id TEXT NOT NULL,
    external_item_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    external_id TEXT NOT NULL,
    PRIMARY KEY (authority_id, external_item_id, namespace, external_id),
    FOREIGN KEY (authority_id, external_item_id)
        REFERENCES inventory_item_origins(authority_id, external_item_id) ON DELETE CASCADE
);
CREATE INDEX idx_inventory_external_ids_grounded
    ON inventory_external_ids (namespace, external_id, item_id);

CREATE TABLE inventory_sources (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    revision TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX idx_inventory_sources_item ON inventory_sources (item_id);

CREATE TABLE inventory_source_origins (
    authority_id TEXT NOT NULL,
    external_item_id TEXT NOT NULL,
    external_source_id TEXT NOT NULL,
    source_id TEXT NOT NULL REFERENCES inventory_sources(id) ON DELETE CASCADE,
    locator_json TEXT NOT NULL,
    observation_json TEXT NOT NULL,
    observed_at BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    missing_at BIGINT,
    PRIMARY KEY (authority_id, external_item_id, external_source_id),
    FOREIGN KEY (authority_id, external_item_id)
        REFERENCES inventory_item_origins(authority_id, external_item_id) ON DELETE CASCADE
);
CREATE INDEX idx_inventory_source_origins_source ON inventory_source_origins (source_id);

CREATE TABLE inventory_source_measurements (
    source_id TEXT PRIMARY KEY REFERENCES inventory_sources(id) ON DELETE CASCADE,
    source_revision TEXT NOT NULL,
    observation_json TEXT NOT NULL,
    observed_at BIGINT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS inventory_source_measurements;
DROP INDEX IF EXISTS idx_inventory_source_origins_source;
DROP TABLE IF EXISTS inventory_source_origins;
DROP INDEX IF EXISTS idx_inventory_sources_item;
DROP TABLE IF EXISTS inventory_sources;
DROP INDEX IF EXISTS idx_inventory_external_ids_grounded;
DROP TABLE IF EXISTS inventory_external_ids;
DROP INDEX IF EXISTS idx_inventory_item_origins_item;
DROP TABLE IF EXISTS inventory_item_origins;
DROP TABLE IF EXISTS inventory_items;
