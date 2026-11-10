-- The pending records of an owner-transfer request. An owner transfer takes
-- effect only after the target confirms it, so the request is a row of its own
-- until then.
--
-- The table deliberately does not embed the tenant scoping model. It is
-- metadata about an organization's lifecycle, like the roster row itself, and
-- the target's confirmation reaches it by an account-level request that carries
-- no tenant context. Every method of the store that reads or writes it
-- therefore carries an explicit tenant or target predicate.
--
-- status is a small closed set: 0 pending, 1 confirmed, 2 declined,
-- 3 cancelled, 4 expired.
CREATE TABLE IF NOT EXISTS tenant_owner_transfers (
    id           TEXT        NOT NULL PRIMARY KEY,
    tenant_id    TEXT        NOT NULL CHECK (tenant_id <> ''),
    from_account TEXT        NOT NULL CHECK (from_account <> ''),
    to_account   TEXT        NOT NULL CHECK (to_account <> ''),
    status       SMALLINT    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2, 3, 4)),
    requested_at TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    resolved_at  TIMESTAMPTZ NULL
);

-- One organization has at most one pending request at a time. Concurrent
-- requests are arbitrated by this index rather than by a read-then-write.
CREATE UNIQUE INDEX IF NOT EXISTS tenant_owner_transfers_one_pending
    ON tenant_owner_transfers (tenant_id) WHERE status = 0;

-- The target account's pending list.
CREATE INDEX IF NOT EXISTS tenant_owner_transfers_target
    ON tenant_owner_transfers (to_account, status);

-- The requester's pending read.
CREATE INDEX IF NOT EXISTS tenant_owner_transfers_tenant
    ON tenant_owner_transfers (tenant_id, status);
