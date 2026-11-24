-- budget_event_id links a challenge to its shared-ledger attempt, so a certain failure refunds both.
ALTER TABLE authn_challenges ADD COLUMN IF NOT EXISTS budget_event_id BIGINT NULL;
