-- The deals of each tenant.
--
-- tenant_id leads the primary key, and identifiers are unique within a tenant
-- alone: two tenants may each hold a deal a client named d-1.
--
-- The stage and the account are references to one of the same tenant's
-- stages and accounts, through the composite keys those tables lead with the
-- tenant column. A deal naming a stage or an account only another tenant has
-- is therefore refused by the database as well as by the look-up the module
-- makes first. The references need the pipeline and accounts migrations
-- applied first, and the deals module declares a requirement on each of those
-- modules' table tokens for that alone; the owner's reference to the users
-- table is ordered the same way, through the users module's token.
--
-- The amount is in the currency's minor units, as a 64-bit integer, so every
-- amount a client can send is stored exactly. version is the deal's revision:
-- every write conditions on it and moves it on. deleted_at marks a deal
-- removed from the pipeline; the row stays, with its activities.
CREATE TABLE deals (
    tenant_id      TEXT      NOT NULL CHECK (tenant_id <> ''),
    id             TEXT      NOT NULL CHECK (id <> ''),
    account_id     TEXT      NOT NULL,
    title          TEXT      NOT NULL,
    amount         BIGINT    NOT NULL CHECK (amount >= 0),
    currency       TEXT      NOT NULL,
    stage_key      TEXT      NOT NULL,
    owner_id       TEXT      NOT NULL REFERENCES users (id),
    version        BIGINT    NOT NULL CHECK (version >= 1),
    expected_close DATE,
    closed_at      TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, stage_key) REFERENCES pipeline_stages (tenant_id, key),
    FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id)
);

-- Every other index leads with the tenant column, so a tenant's query reads
-- that tenant's part of the index alone.
CREATE INDEX deals_stage_idx ON deals (tenant_id, stage_key);
CREATE INDEX deals_owner_idx ON deals (tenant_id, owner_id);
CREATE INDEX deals_account_idx ON deals (tenant_id, account_id);
CREATE INDEX deals_created_idx ON deals (tenant_id, created_at);
