-- The stages of each tenant's pipeline.
--
-- tenant_id leads the primary key for the reason it does on every scoped table
-- of this host: two tenants each have a stage of one key, and a key without the
-- tenant beside it would let one tenant's insert meet the other's row. It is
-- also what the deals table's reference names, so a deal's stage is one of its
-- own tenant's stages by construction.
--
-- The CHECK on tenant_id catches a statement issued where the tenant filter is
-- not installed, which would write an empty tenant rather than fail.
CREATE TABLE pipeline_stages (
    tenant_id   TEXT    NOT NULL CHECK (tenant_id <> ''),
    key         TEXT    NOT NULL CHECK (key <> ''),
    name        TEXT    NOT NULL,
    position    INTEGER NOT NULL CHECK (position >= 0),
    probability INTEGER NOT NULL CHECK (probability BETWEEN 0 AND 100),
    kind        TEXT    NOT NULL CHECK (kind IN ('open', 'won', 'lost')),
    created_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, key)
);
