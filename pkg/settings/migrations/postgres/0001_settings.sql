-- The settings: one row per key per layer.
--
-- An empty tenant_id is the global row, the layer every tenant without a row
-- of its own falls back to. It sits in the same key space as the tenant rows
-- rather than in a table of its own, so the primary key covers "at most one
-- row per key per layer" and the two layers cannot drift apart.
CREATE TABLE IF NOT EXISTS settings (
    tenant_id  TEXT        NOT NULL DEFAULT '',
    key        TEXT        NOT NULL,
    value      TEXT        NOT NULL,
    version    BIGINT      NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, key)
);
