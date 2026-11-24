ALTER TABLE sms_budget_events ADD COLUMN IF NOT EXISTS suppressed BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS sms_budget_events_account_attempted
    ON sms_budget_events (account_id, attempted_at);
