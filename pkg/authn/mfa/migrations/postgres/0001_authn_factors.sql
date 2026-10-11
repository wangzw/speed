-- The factor register: one row per second factor an account has registered,
-- whatever its kind. Only the data a kind needs of its own, a TOTP seed or a
-- passkey's credential, lives in a table of that kind's own module.
--
-- status is NOT NULL with a default of zero, and zero is "pending": a row whose
-- status was never written is not used for verification, which is the direction
-- a value nobody chose has to fall. There is no foreign key: whether the account
-- exists is the caller's to check, and the rows go with it through the account
-- lifecycle hook. Every time is UTC.
CREATE TABLE IF NOT EXISTS authn_factors (
    id           TEXT        NOT NULL PRIMARY KEY,
    account_id   TEXT        NOT NULL,
    kind         TEXT        NOT NULL CHECK (kind IN ('totp', 'sms', 'webauthn')),
    label        TEXT        NOT NULL DEFAULT '' CHECK (length(label) <= 64),
    status       SMALLINT    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    created_at   TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ NULL,
    last_used_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS authn_factors_account ON authn_factors (account_id, status);
