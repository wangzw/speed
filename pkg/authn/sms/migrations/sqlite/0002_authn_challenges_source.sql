-- The source dimension is a keyed digest of the client address. null means no trustworthy origin and spends no source budget.
-- SQLite has no ADD COLUMN IF NOT EXISTS. a migration is applied once and recorded.
ALTER TABLE authn_challenges ADD COLUMN source_mac TEXT NULL;
CREATE INDEX IF NOT EXISTS authn_challenges_source ON authn_challenges (source_mac, created_at);
