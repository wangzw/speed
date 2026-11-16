-- The invitation store: one row per invitation, from pending to a terminal
-- state. An invitation is a pending grant of a membership: it names an address
-- and a tenant and, until it is accepted, no account or membership row exists.
--
-- No table has a foreign key. A column that names an account or a tenant is
-- only a column, and the writer of a row keeps it whole in its own transaction.
-- Every time is stored in UTC. Only the SHA-256 of the acceptance token is
-- kept, never the token itself.
--
-- revoked_at is when the invitation was revoked. Every revocation path writes
-- it: an administrator revoking, and a system path revoking (a member disabled
-- or removed, an organization deleted, an acceptance that failed to settle).
-- NULL means the row was never revoked.
--
-- last_sent_at is when the invitation last went out. Issuance always writes it,
-- so the column is never NULL. send_count counts every send including the first
-- issuance, which the column default records as one.
--
-- superseded_at is when a later invitation replaced this one. An expired row
-- stays expired rather than being revoked, and the new row for the same address
-- records the replacement here instead. NULL means no later invitation replaced
-- it.
--
-- role is the role the invitation grants beside the membership. The empty
-- string means the invitation grants no role, so a row written before roles
-- existed still reads as one without a role. The column is part of the row so
-- that a resend keeps the role: only the token changes on a resend.

CREATE TABLE IF NOT EXISTS authn_invitations (
    id            TEXT        NOT NULL PRIMARY KEY,
    tenant_id     TEXT        NOT NULL CHECK (tenant_id <> ''),
    email         TEXT        NOT NULL,
    token_digest  TEXT        NOT NULL,
    status        SMALLINT    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    invited_by    TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT '',
    expires_at    TIMESTAMPTZ NOT NULL,
    accepted_at   TIMESTAMPTZ NULL,
    revoked_at    TIMESTAMPTZ NULL,
    last_sent_at  TIMESTAMPTZ NOT NULL,
    send_count    INTEGER     NOT NULL DEFAULT 1,
    superseded_at TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (token_digest)
);

-- The listing of one tenant, newest first.
CREATE INDEX IF NOT EXISTS authn_invitations_tenant ON authn_invitations (tenant_id, created_at);

-- At most one live invitation per tenant and address: one row that is pending
-- and not replaced by a later invitation. A new issuance of the same pair
-- supersedes the old row in the same transaction, and a concurrent issuance
-- collides here rather than leaving two live tokens behind. A row a later
-- invitation replaced is history rather than a live slot.
CREATE UNIQUE INDEX IF NOT EXISTS authn_invitations_one_pending
    ON authn_invitations (tenant_id, email)
    WHERE status = 0 AND superseded_at IS NULL;
