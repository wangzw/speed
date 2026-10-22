-- Contact claims: authn_pending_contacts is the reservation table for an email
-- address or a phone number.
--
-- A row whose verified_at is empty is an unproved reservation a self-registration
-- wrote before the address was proved. A row whose verified_at is set is a
-- contact an account proved or an administrator wrote, and its row stays after
-- the proof. The unique index on (kind, value) is what arbitrates a
-- registration: the transaction that inserts the row owns the contact, the one
-- that loses the insert is refused, and acceptance of an unproved reservation is
-- deliberate. An unverified reservation becomes a held contact when the account
-- proves the address, is released when the account removes it, or is deleted with
-- the account by the unverified-account sweep in the same transaction.
-- An account may reserve several addresses, so the reservation is unique by
-- contact and not by account.
--
-- No data migration is done: a database whose existing rows break the unique
-- index is rebuilt rather than reconciled.
--
-- SQLite cannot drop a table constraint in place, so the table is rebuilt with
-- the verified_at column and without the per-account uniqueness. The rows that
-- are there are carried over as they are.

CREATE TABLE authn_pending_contacts_claims (
    id          TEXT      NOT NULL PRIMARY KEY,
    account_id  TEXT      NOT NULL,
    kind        TEXT      NOT NULL CHECK (kind IN ('email', 'phone')),
    value       TEXT      NOT NULL,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL,
    verified_at TIMESTAMP NULL
);

INSERT INTO authn_pending_contacts_claims (id, account_id, kind, value, created_at, updated_at, verified_at)
    SELECT id, account_id, kind, value, created_at, updated_at, NULL FROM authn_pending_contacts;

DROP TABLE authn_pending_contacts;

ALTER TABLE authn_pending_contacts_claims RENAME TO authn_pending_contacts;

-- The non-unique lookup index becomes the global unique arbiter of a contact.
DROP INDEX IF EXISTS authn_pending_contacts_value;
CREATE UNIQUE INDEX IF NOT EXISTS authn_pending_contacts_value ON authn_pending_contacts (kind, value);
