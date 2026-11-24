-- The factor register: one row per second factor an account has.
-- status 0 is pending and is never used to verify. there is no foreign key.
-- SQLite compares a time as text in the zone it carries, so the module writes and binds nothing but UTC.
CREATE TABLE IF NOT EXISTS authn_factors (
    id           TEXT      NOT NULL PRIMARY KEY,
    account_id   TEXT      NOT NULL,
    kind         TEXT      NOT NULL CHECK (kind IN ('totp', 'sms', 'webauthn')),
    label        TEXT      NOT NULL DEFAULT '' CHECK (length(label) <= 64),
    status       SMALLINT  NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    created_at   TIMESTAMP NOT NULL,
    activated_at TIMESTAMP NULL,
    last_used_at TIMESTAMP NULL
);
CREATE INDEX IF NOT EXISTS authn_factors_account ON authn_factors (account_id, status);
