-- The object_grants table: one row per direct-access address this process
-- minted for a local-directory backend. It is purely additive; no existing row
-- is read or rewritten.
--
-- The tenant column leads the key, as it does on every scoped table: the model
-- embeds tenant.Model. The token hash is the second key column and is unique on
-- its own, which is what lets the content route find a grant before it knows the
-- tenant: the token names one.
--
-- Only the SHA-256 of the token is stored, never the token itself, so the table
-- holds no value that can be used as a credential. Every instant is milliseconds
-- since the Unix epoch, as on the objects table, so the content route and the
-- sweep judge an expiry against the database's own clock on either engine.
CREATE TABLE IF NOT EXISTS object_grants (
    tenant_id  TEXT    NOT NULL,
    token_hash TEXT    NOT NULL,
    object_id  TEXT    NOT NULL,
    use        TEXT    NOT NULL CHECK (use IN ('read', 'write')),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, token_hash)
);

CREATE UNIQUE INDEX IF NOT EXISTS object_grants_token_idx ON object_grants (token_hash);

CREATE INDEX IF NOT EXISTS object_grants_expires_idx ON object_grants (expires_at);
