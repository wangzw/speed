-- One row per deal event delivered to a tenant's webhook: where it went, how
-- many attempts it has taken and how it ended.
--
-- tenant_id leads the primary key, as on every scoped table: an event
-- identifier is unique only as far as its publisher makes it so, and keyed by
-- the event alone, a delivery recorded for one tenant would be the row another
-- tenant's delivery of the same identifier finds. The key is the one the Go
-- model declares: tenant.Model contributes tenant_id, and WebhookDelivery marks
-- event_id beside it.
--
-- The CHECK on tenant_id catches the one write the NOT NULL does not: a
-- statement issued where the tenant filter is not installed writes an empty
-- tenant rather than failing.
CREATE TABLE webhook_deliveries (
    tenant_id     TEXT    NOT NULL CHECK (tenant_id <> ''),
    event_id      TEXT    NOT NULL,
    event_type    TEXT    NOT NULL,
    url           TEXT    NOT NULL,
    status        TEXT    NOT NULL CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts      INTEGER NOT NULL CHECK (attempts >= 0),
    response_code INTEGER NOT NULL,
    last_error    TEXT    NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, event_id)
);

-- The listing a tenant reads, newest first. It leads with the tenant column,
-- as every index on a scoped table does.
CREATE INDEX webhook_deliveries_recent_idx ON webhook_deliveries (tenant_id, created_at);
