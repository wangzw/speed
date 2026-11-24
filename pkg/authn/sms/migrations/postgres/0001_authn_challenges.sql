-- Suppressed rows count toward the number, source and account budgets, and only the deployment-wide budget skips them.
-- Neither the number nor the code is stored: phone_mac and code_mac are keyed digests that reveal nothing without the root key.
-- account_id is empty for login and filled for mfa and enroll. There is no foreign key, and every time is UTC.
CREATE TABLE IF NOT EXISTS authn_challenges (
    id           TEXT        NOT NULL PRIMARY KEY,
    purpose      TEXT        NOT NULL CHECK (purpose IN ('login', 'mfa', 'enroll')),
    phone_mac    TEXT        NOT NULL,
    account_id   TEXT        NULL,
    suppressed   BOOLEAN     NOT NULL DEFAULT FALSE,
    code_mac     TEXT        NOT NULL,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    max_attempts INTEGER     NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    consumed_at  TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS authn_challenges_phone ON authn_challenges (phone_mac, created_at);
CREATE INDEX IF NOT EXISTS authn_challenges_expires ON authn_challenges (expires_at);
