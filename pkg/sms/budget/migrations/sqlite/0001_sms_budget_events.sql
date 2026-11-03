-- One row per SMS attempt, whichever sending channel made it. The row is the
-- budget ledger: both the authentication capability and the notification
-- capability write here and count here, so the cross-channel ceiling and a
-- source's distinct destinations are measured over one set of rows.
--
-- An attempt is written when its budget is spent, before the carrier answers.
-- outcome says what was known then; refunded_at is the state that changes
-- later. A refunded row leaves every send count and stays in every attempt
-- count, which is how a message the vendor refused stops spending a real send
-- budget without handing the caller a fresh attempt budget.
--
-- Neither the source identity nor the destination is stored: source_mac and
-- destination_mac are the caller's keyed digests, and the plaintext never
-- reaches this table. source_mac is nullable because an attempt with no
-- trustworthy origin has no source; its null is meaningful and takes no source
-- budget. account_id is nullable for the same reason: the login and reset
-- purposes have no account. tenant_id is not nullable; an attempt that is not
-- scoped to a tenant carries the empty string, which no tenant count matches.
-- Every time is UTC. SQLite compares a time as text in the zone it carries, so
-- the store binds nothing but UTC.
CREATE TABLE IF NOT EXISTS sms_budget_events (
    id              INTEGER   PRIMARY KEY AUTOINCREMENT,
    channel         TEXT      NOT NULL CHECK (channel IN ('authn', 'notification')),
    purpose         TEXT      NOT NULL,
    source_mac      TEXT      NULL,
    destination_mac TEXT      NOT NULL,
    tenant_id       TEXT      NOT NULL DEFAULT '',
    account_id      TEXT      NULL,
    attempted_at    TIMESTAMP NOT NULL,
    outcome         TEXT      NOT NULL,
    refunded_at     TIMESTAMP NULL
);

-- The channel and purpose filters of every count walk (channel, attempted_at);
-- the cross-channel ceiling names both channels, so it reads the same index
-- once per channel.
CREATE INDEX IF NOT EXISTS sms_budget_events_channel_attempted
    ON sms_budget_events (channel, attempted_at);

-- The per-source send, attempt and distinct-destination budgets walk
-- (source_mac, attempted_at).
CREATE INDEX IF NOT EXISTS sms_budget_events_source_attempted
    ON sms_budget_events (source_mac, attempted_at);

-- The per-destination budget walks (destination_mac, attempted_at).
CREATE INDEX IF NOT EXISTS sms_budget_events_destination_attempted
    ON sms_budget_events (destination_mac, attempted_at);

-- The notification capability's per-tenant fan-out walks
-- (tenant_id, attempted_at).
CREATE INDEX IF NOT EXISTS sms_budget_events_tenant_attempted
    ON sms_budget_events (tenant_id, attempted_at);
