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

CREATE INDEX IF NOT EXISTS sms_budget_events_channel_attempted
    ON sms_budget_events (channel, attempted_at);

CREATE INDEX IF NOT EXISTS sms_budget_events_source_attempted
    ON sms_budget_events (source_mac, attempted_at);

CREATE INDEX IF NOT EXISTS sms_budget_events_destination_attempted
    ON sms_budget_events (destination_mac, attempted_at);

CREATE INDEX IF NOT EXISTS sms_budget_events_tenant_attempted
    ON sms_budget_events (tenant_id, attempted_at);
