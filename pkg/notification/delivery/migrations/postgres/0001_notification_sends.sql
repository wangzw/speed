-- The delivery ledger: one row per channel attempt, an operations view rather than a contract face.
-- Every instant is milliseconds since the Unix epoch so both engines carry the same number, and tenant_id leads the key as on every scoped table.
-- The unique index on (tenant_id, idempotency_key) makes the ledger idempotent: one attempt at one logical event derives one key, and a second updates the row.
CREATE TABLE IF NOT EXISTS notification_sends (
    id                  TEXT        NOT NULL,
    tenant_id           TEXT        NOT NULL,
    type_key            TEXT        NOT NULL,
    channel             TEXT        NOT NULL CHECK (channel IN ('in_app', 'email', 'sms')),
    recipient_class     TEXT        NOT NULL,
    recipient_user_id   TEXT        NOT NULL,
    status              TEXT        NOT NULL CHECK (status IN ('succeeded', 'failed', 'skipped')),
    duration_ms         BIGINT      NOT NULL,
    error               TEXT,
    provider_receipt_id TEXT,
    idempotency_key     TEXT        NOT NULL,
    created_at          BIGINT      NOT NULL,
    updated_at          BIGINT      NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS notification_sends_tenant_idempotency_idx
    ON notification_sends (tenant_id, idempotency_key);
