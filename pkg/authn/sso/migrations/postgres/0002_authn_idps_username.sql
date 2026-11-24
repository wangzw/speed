-- How a just-in-time login determines the username it opens an account with.
-- A provider that never provisions keeps both columns empty. the application refuses an incomplete combination on every write.
-- Comments here must not contain a semicolon: the module's tests split a migration on them.
ALTER TABLE authn_idps ADD COLUMN IF NOT EXISTS username_strategy TEXT NOT NULL DEFAULT '';
ALTER TABLE authn_idps ADD COLUMN IF NOT EXISTS username_claim TEXT NOT NULL DEFAULT '';
