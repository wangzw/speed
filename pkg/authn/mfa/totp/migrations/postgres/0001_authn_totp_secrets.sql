-- TOTP seeds keyed by the factor row. secret is an encrypted column.
-- last_counter is the greatest counter accepted, so a code whose counter is not above it is a replay.
CREATE TABLE IF NOT EXISTS authn_totp_secrets (
    factor_id    TEXT        NOT NULL PRIMARY KEY,
    secret       TEXT        NOT NULL,
    algorithm    TEXT        NOT NULL DEFAULT 'SHA1',
    digits       INTEGER     NOT NULL DEFAULT 6,
    period       INTEGER     NOT NULL DEFAULT 30,
    last_counter BIGINT      NOT NULL DEFAULT 0
);
