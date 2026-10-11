-- The refresh tokens of the token sessions. One row per issued token, with the
-- digest of the token and no plaintext. No foreign key: session_id names a
-- session without referencing a table of another module, and every time is UTC.
CREATE TABLE IF NOT EXISTS authn_refresh_tokens (
    id              TEXT        NOT NULL PRIMARY KEY,
    session_id      TEXT        NOT NULL,                    -- the family is the session
    token_hash      TEXT        NOT NULL UNIQUE,
    parent_id       TEXT        NULL,
    issued_at       TIMESTAMPTZ NOT NULL,
    idle_expires_at TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ NULL,
    replaced_by     TEXT        NULL
);

CREATE INDEX IF NOT EXISTS authn_refresh_tokens_session ON authn_refresh_tokens (session_id);
CREATE INDEX IF NOT EXISTS authn_refresh_tokens_expiry  ON authn_refresh_tokens (idle_expires_at);
