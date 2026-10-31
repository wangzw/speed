-- A registered address is a contact. Every email address and phone number
-- written before this invariant was enforced becomes one, and no other stored
-- fact changes.
--
-- 0004 backfilled contact for email addresses only, so a phone number written
-- under the old behaviour reads contact false until this migration. The
-- backfill writes the contact column alone: sign_in, default_contact and every
-- fact column stay as they are.

UPDATE authn_identifiers SET contact = TRUE WHERE kind IN ('email', 'phone');
