-- One row per SMS verification challenge the module has issued, whatever its
-- purpose: login, mfa or enroll. The row is the whole of the challenge state —
-- the budgets are counts over it, the check reads it, and nothing else holds a
-- challenge.
--
-- Neither the phone number nor the code is stored: phone_mac is the keyed digest
-- of the normalized E.164 number, and code_mac is the keyed digest of the
-- challenge identifier and the code together. Both are computed through the
-- database module's blind index, so the searchable column carries a value that
-- reveals nothing without the root key.
--
-- account_id is empty for the login purpose, which has no account to count
-- against, and filled for mfa and enroll, which do. There is no foreign key:
-- whether the account exists is the caller's to check, and the rows go with it
-- through the account lifecycle hook. Every time is UTC.
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

-- The phone dimension of every budget walks (phone_mac, created_at).
CREATE INDEX IF NOT EXISTS authn_challenges_phone ON authn_challenges (phone_mac, created_at);

-- The sweep removes rows past the counting window by expires_at.
CREATE INDEX IF NOT EXISTS authn_challenges_expires ON authn_challenges (expires_at);
