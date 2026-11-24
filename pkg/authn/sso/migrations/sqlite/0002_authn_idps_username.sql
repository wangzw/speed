-- How a just-in-time login determines the username it opens an account with.
-- A provider that never provisions keeps both columns empty. the application refuses an incomplete combination on every write.
-- SQLite has no ADD COLUMN IF NOT EXISTS, and the closed sets are held by the application rather than a CHECK.
ALTER TABLE authn_idps ADD COLUMN username_strategy TEXT NOT NULL DEFAULT '';
ALTER TABLE authn_idps ADD COLUMN username_claim TEXT NOT NULL DEFAULT '';
