-- A row the carrier certainly did not take is refunded and spent, so every budget stops counting it. an uncertain outcome is not refunded.
-- SQLite has no ADD COLUMN IF NOT EXISTS. a migration is applied once and recorded.
ALTER TABLE authn_challenges ADD COLUMN refunded_at TIMESTAMP NULL;
