-- How a just-in-time login determines the username it opens an account with.
-- A provider that never provisions keeps both columns empty. A just-in-time
-- provider names one of claim and user-confirm, and a claim strategy names the
-- claim it reads. The application refuses an incomplete combination on every
-- write, and the closed sets are held there rather than in a CHECK an added
-- column cannot carry on this engine.
--
-- SQLite has no ADD COLUMN IF NOT EXISTS. A migration is applied once and
-- recorded, so the plain ADD COLUMN is what a fresh database and an upgrade both
-- run exactly once. Rows written before these columns existed keep the empty
-- values, and a just-in-time row among them is refused at login until an
-- administrator completes it: the old system is deleted and rebuilt.
ALTER TABLE authn_idps ADD COLUMN username_strategy TEXT NOT NULL DEFAULT '';

ALTER TABLE authn_idps ADD COLUMN username_claim TEXT NOT NULL DEFAULT '';
