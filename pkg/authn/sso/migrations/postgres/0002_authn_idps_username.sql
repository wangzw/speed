-- How a just-in-time login determines the username it opens an account with.
-- A provider that never provisions keeps both columns empty. A just-in-time
-- provider names one of claim and user-confirm, and a claim strategy names the
-- claim it reads. The application refuses an incomplete combination on every
-- write, which is where the closed sets are held on both engines. The comments
-- here hold no semicolon: the module's own tests split a migration on them.
--
-- Rows written before these columns existed keep the empty values, and a
-- just-in-time row among them is refused at login until an administrator
-- completes it: the old system is deleted and rebuilt.
ALTER TABLE authn_idps ADD COLUMN IF NOT EXISTS username_strategy TEXT NOT NULL DEFAULT '';

ALTER TABLE authn_idps ADD COLUMN IF NOT EXISTS username_claim TEXT NOT NULL DEFAULT '';
