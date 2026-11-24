ALTER TABLE sms_budget_events ADD COLUMN suppressed BOOLEAN NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS sms_budget_events_account_attempted
    ON sms_budget_events (account_id, attempted_at);
