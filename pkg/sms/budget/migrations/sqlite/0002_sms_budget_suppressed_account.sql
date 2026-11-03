-- The suppressed marker and the account dimension's index.
--
-- suppressed marks a message the caller deliberately did not send: it is
-- refunded at write time, so it spends no send budget and no share of the
-- cross-channel ceiling, and it takes part in a count only when the query asks
-- for it. A row written before this column existed was an attempt the caller
-- meant to send, so it takes the false default.
--
-- account_id already exists on the table; the index is what makes the
-- per-account budget a walk instead of a scan. SQLite has no ADD COLUMN IF NOT
-- EXISTS. A migration is applied once and recorded, so the plain ADD COLUMN is
-- what a fresh database and an upgrade both need.
ALTER TABLE sms_budget_events ADD COLUMN suppressed BOOLEAN NOT NULL DEFAULT 0;

-- The per-account budget walks (account_id, attempted_at). The four indexes
-- from the first migration stay as they were.
CREATE INDEX IF NOT EXISTS sms_budget_events_account_attempted
    ON sms_budget_events (account_id, attempted_at);
