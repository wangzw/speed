-- Identifier axes: sign_in and contact say what an identifier is used for, and
-- default_contact marks the address a notification to the account goes to.
--
-- Signing in and being a contact are two independent axes of one identifier, and
-- proving ownership decides neither: an identifier can be a sign-in way, a
-- contact, both or neither. default_contact is separate again and is set only on
-- a proved email address.
--
-- Rows written before these columns existed keep their behaviour: every
-- identifier written under the old behaviour is a sign-in way, every email
-- identifier among them is also a contact, and the earliest proved email address
-- of an account (ties broken by identifier) becomes its default contact. The
-- backfill writes these three columns only. verified_at, verified_via and every
-- other stored fact stay as they are, so an old email still signs in and is a
-- contact exactly as it was used.
--
-- New writes start with all three false and choose the axes themselves.

ALTER TABLE authn_identifiers ADD COLUMN IF NOT EXISTS sign_in BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE authn_identifiers ADD COLUMN IF NOT EXISTS contact BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE authn_identifiers ADD COLUMN IF NOT EXISTS default_contact BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE authn_identifiers SET sign_in = TRUE;
UPDATE authn_identifiers SET contact = TRUE WHERE kind = 'email';

-- The earliest proved email address of each account becomes its default
-- contact. admin, import-unverified and self record an assertion, not a proof,
-- so they never qualify.
UPDATE authn_identifiers SET default_contact = TRUE
WHERE kind = 'email'
  AND verified_via NOT IN ('admin', 'import-unverified', 'self')
  AND id = (
      SELECT earliest.id
      FROM authn_identifiers AS earliest
      WHERE earliest.account_id = authn_identifiers.account_id
        AND earliest.kind = 'email'
        AND earliest.verified_via NOT IN ('admin', 'import-unverified', 'self')
      ORDER BY earliest.verified_at, earliest.id
      LIMIT 1
  );

-- At most one default contact per account, and the writer keeps exactly one
-- while the account has a proved email address.
CREATE UNIQUE INDEX IF NOT EXISTS authn_identifiers_default_contact
    ON authn_identifiers (account_id) WHERE default_contact AND kind = 'email';
