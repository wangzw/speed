-- The login sessions: browser cookies, token sessions and the one row of each
-- service-account secret. No foreign key: account_id names an account without
-- referencing a table of another module, and every time is UTC.
CREATE TABLE IF NOT EXISTS authn_sessions (
    id               TEXT        NOT NULL PRIMARY KEY,
    token_hash       TEXT        NULL UNIQUE,
    account_id       TEXT        NOT NULL,
    kind             TEXT        NOT NULL CHECK (kind IN ('browser', 'token', 'service')),
    credential_id    TEXT        NULL UNIQUE,
    impersonation_id TEXT        NULL,
    data             TEXT        NOT NULL CHECK (length(data) <= 8192),
    client_ip        TEXT        NOT NULL DEFAULT '',
    user_agent       TEXT        NOT NULL DEFAULT '' CHECK (length(user_agent) <= 200),
    created_at       DATETIME    NOT NULL,
    last_seen_at     DATETIME    NOT NULL,
    idle_expires_at  DATETIME    NULL,
    expires_at       DATETIME    NULL,
    revoked_at       DATETIME    NULL,
    revoke_reason    TEXT        NULL,
    CHECK ((kind = 'browser' AND idle_expires_at IS NOT NULL AND expires_at IS NOT NULL)
        OR (kind = 'token'   AND idle_expires_at IS NULL     AND expires_at IS NOT NULL)
        OR (kind = 'service' AND idle_expires_at IS NULL))
);

CREATE INDEX IF NOT EXISTS authn_sessions_account ON authn_sessions (account_id, revoked_at);
CREATE INDEX IF NOT EXISTS authn_sessions_expires ON authn_sessions (expires_at);
