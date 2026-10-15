-- The invitation store: one row per invitation, from pending to a terminal
-- state. An invitation is a pending grant of a membership: it names an address
-- and a tenant and, until it is accepted, no account or membership row exists.
--
-- No table has a foreign key. A column that names an account or a tenant is
-- only a column, and the writer of a row keeps it whole in its own transaction.
-- Every time is stored in UTC. Only the SHA-256 of the acceptance token is
-- kept, never the token itself.

CREATE TABLE IF NOT EXISTS authn_invitations (
    id           TEXT        NOT NULL PRIMARY KEY,
    tenant_id    TEXT        NOT NULL CHECK (tenant_id <> ''),
    email        TEXT        NOT NULL,
    token_digest TEXT        NOT NULL,
    status       SMALLINT    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    invited_by   TEXT        NOT NULL,
    expires_at   TIMESTAMP   NOT NULL,
    accepted_at  TIMESTAMP   NULL,
    created_at   TIMESTAMP   NOT NULL,
    updated_at   TIMESTAMP   NOT NULL,
    UNIQUE (token_digest)
);

-- The listing of one tenant, newest first.
CREATE INDEX IF NOT EXISTS authn_invitations_tenant ON authn_invitations (tenant_id, created_at);

-- At most one pending invitation per tenant and address. A new issuance of the
-- same pair supersedes the old row in the same transaction, and a concurrent
-- issuance collides here rather than leaving two live tokens behind.
CREATE UNIQUE INDEX IF NOT EXISTS authn_invitations_one_pending ON authn_invitations (tenant_id, email) WHERE status = 0;
