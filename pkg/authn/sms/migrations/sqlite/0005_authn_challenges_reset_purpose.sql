-- The reset purpose joins the closed set a challenge row may carry: an anonymous
-- password reset proves the holder of a number through the same state machine as
-- the other purposes, so its rows share the table. A reset row fills no account
-- and takes no account lock, but that is the caller's shape, not the column's.
--
-- SQLite cannot alter a CHECK constraint in place, so the table is rebuilt with
-- the widened set and its rows copied across. Dropping the old table drops every
-- index with it, so all of them are recreated.
CREATE TABLE authn_challenges_reset_purpose (
    id           TEXT      NOT NULL PRIMARY KEY,
    purpose      TEXT      NOT NULL CHECK (purpose IN ('login', 'mfa', 'enroll', 'reset')),
    phone_mac    TEXT      NOT NULL,
    account_id   TEXT      NULL,
    suppressed   BOOLEAN   NOT NULL DEFAULT 0,
    code_mac     TEXT      NOT NULL,
    attempts     INTEGER   NOT NULL DEFAULT 0,
    max_attempts INTEGER   NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP NOT NULL,
    consumed_at  TIMESTAMP NULL,
    source_mac   TEXT      NULL,
    refunded_at  TIMESTAMP NULL
);

INSERT INTO authn_challenges_reset_purpose
    (id, purpose, phone_mac, account_id, suppressed, code_mac, attempts, max_attempts,
     created_at, expires_at, consumed_at, source_mac, refunded_at)
SELECT id, purpose, phone_mac, account_id, suppressed, code_mac, attempts, max_attempts,
       created_at, expires_at, consumed_at, source_mac, refunded_at
FROM authn_challenges;

DROP TABLE authn_challenges;

ALTER TABLE authn_challenges_reset_purpose RENAME TO authn_challenges;

CREATE INDEX IF NOT EXISTS authn_challenges_phone ON authn_challenges (phone_mac, created_at);
CREATE INDEX IF NOT EXISTS authn_challenges_expires ON authn_challenges (expires_at);
CREATE INDEX IF NOT EXISTS authn_challenges_source ON authn_challenges (source_mac, created_at);
CREATE INDEX IF NOT EXISTS authn_challenges_purpose_suppressed
    ON authn_challenges (purpose, suppressed, created_at);
CREATE INDEX IF NOT EXISTS authn_challenges_account ON authn_challenges (account_id, created_at);
