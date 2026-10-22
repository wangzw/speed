-- Self-registration: the state an account's registration has reached, and the
-- contacts it lists as awaiting verification.
--
-- An account created by Signups.Register is enabled and holds signup_state 1.
-- The first email identifier written through a proving channel moves it to 2
-- and records when that happened. Removing the last proved email address moves
-- it back to 1 and keeps the instant. No column here enters a session, an
-- identity or a token.

ALTER TABLE authn_accounts ADD COLUMN signup_state SMALLINT NOT NULL DEFAULT 0 CHECK (signup_state IN (0, 1, 2));
ALTER TABLE authn_accounts ADD COLUMN signup_verified_at TIMESTAMP NULL;

-- The scan of the sweep that deletes self-registered accounts that never proved
-- an address.
CREATE INDEX IF NOT EXISTS authn_accounts_signup ON authn_accounts (signup_state, created_at);

-- A contact awaiting verification is not an identifier: no login name, lookup,
-- automatic link or enterprise merge reads this table. An account lists at most
-- one contact of each kind. One address may be the pending target of several
-- accounts, so there is no global unique constraint.
CREATE TABLE IF NOT EXISTS authn_pending_contacts (
    id         TEXT      NOT NULL PRIMARY KEY,
    account_id TEXT      NOT NULL,
    kind       TEXT      NOT NULL CHECK (kind IN ('email', 'phone')),
    value      TEXT      NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (account_id, kind)
);

-- The reverse lookup PendingAccountByContact reads.
CREATE INDEX IF NOT EXISTS authn_pending_contacts_value ON authn_pending_contacts (kind, value);
