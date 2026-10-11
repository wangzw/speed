-- The companies a tenant sells to.
--
-- tenant_id leads the primary key, for the reason the memberships table gives:
-- keyed by the identifier alone, an insert from one tenant that met another
-- tenant's row would update that row into the writer's tenant, through a
-- statement that names no other tenant. Leading with it also lets two tenants
-- choose the same identifier, which is what their records are to each of them.
-- The CHECK catches a write issued where the tenant filter is not installed,
-- which writes an empty tenant rather than failing.
--
-- owner is the account identifier of the member who owns this record. It names
-- no foreign key: the identity system's accounts live in another module's
-- tables, and a cross-module reference would couple the two migrations' order.
--
-- deleted_at is the soft delete: a deleted account keeps its row, and every
-- read through the model leaves it out.
CREATE TABLE accounts (
    tenant_id  TEXT        NOT NULL CHECK (tenant_id <> ''),
    id         TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    domain     TEXT        NOT NULL,
    industry   TEXT        NOT NULL,
    owner      TEXT        NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME,
    PRIMARY KEY (tenant_id, id)
);

-- The listing's order, within one tenant.
CREATE INDEX accounts_name_idx ON accounts (tenant_id, name, id);
