-- budget_event_id links a challenge to its shared-ledger attempt, so a certain failure refunds both.
-- SQLite has no ADD COLUMN IF NOT EXISTS. a migration is applied once and recorded.
ALTER TABLE authn_challenges ADD COLUMN budget_event_id INTEGER NULL;
