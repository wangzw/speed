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

ALTER TABLE authn_pending_contacts ADD COLUMN IF NOT EXISTS verified_at TIMESTAMPTZ NULL;

-- The non-unique lookup index becomes the global unique arbiter of a contact.
DROP INDEX IF EXISTS authn_pending_contacts_value;
CREATE UNIQUE INDEX IF NOT EXISTS authn_pending_contacts_value ON authn_pending_contacts (kind, value);

-- The former one-contact-per-kind rule no longer holds.
ALTER TABLE authn_pending_contacts DROP CONSTRAINT IF EXISTS authn_pending_contacts_account_id_kind_key;
