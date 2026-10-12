-- The authorization policy: roles, permission grants and role bindings.
--
-- Every table leads its key with tenant_id, a real column, so that the tenant
-- filter and the scoped models recognise the tenant rather than reading it out
-- of a policy string. The column is non-empty: a row belonging to no tenant
-- would reach no tenant's decision while the cross-tenant mark let it through.
--
-- There are no foreign keys to the authentication tables: a cross-module key
-- would make the order of two modules' migrations a hidden contract, and an
-- account's deletion is cleaned up through the lifecycle hook instead.
CREATE TABLE authz_roles (
    tenant_id       TEXT NOT NULL CHECK (tenant_id <> ''),
    role            TEXT NOT NULL,
    builtin         BOOLEAN NOT NULL,
    description_key TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, role)
);

CREATE TABLE authz_grants (
    tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
    role       TEXT NOT NULL,
    resource   TEXT NOT NULL,
    action     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, role, resource, action)
);

CREATE INDEX authz_grants_by_object ON authz_grants (tenant_id, resource, action);

CREATE TABLE authz_bindings (
    tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
    account_id TEXT NOT NULL,
    role       TEXT NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, account_id, role)
);

CREATE INDEX authz_bindings_by_role ON authz_bindings (tenant_id, role);
