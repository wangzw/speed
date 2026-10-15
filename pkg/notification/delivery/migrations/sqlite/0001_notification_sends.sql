-- The delivery ledger: one row per channel attempt at delivering one dispatch.
-- It is this module's own table and no other module reads or writes it; its
-- columns are an operations view of what went out, not a contract face.
--
-- The tenant column leads the key, as it does on every scoped table: the model
-- embeds tenant.Model, and the key the convention declares is the key the table
-- must declare. It is not null, so a row that belongs to no tenant cannot land
-- here.
--
-- Every instant is milliseconds since the Unix epoch rather than a timestamp
-- type: the module writes and compares them through one model on either engine,
-- and an integer is the same on both while a timestamp literal is not.
--
-- The unique index on (tenant_id, idempotency_key) is what makes the ledger
-- idempotent: one channel's attempt at one logical event derives one key, so a
-- second attempt at the same key updates the row rather than writing a second
-- one. The key already carries the tenant, but the index leads with the tenant
-- column so a single tenant's lookup does not scan the table.
CREATE TABLE IF NOT EXISTS notification_sends (
    id                  TEXT    NOT NULL,
    tenant_id           TEXT    NOT NULL,
    type_key            TEXT    NOT NULL,
    channel             TEXT    NOT NULL CHECK (channel IN ('in_app', 'email', 'sms')),
    recipient_class     TEXT    NOT NULL,
    recipient_user_id   TEXT    NOT NULL,
    status              TEXT    NOT NULL CHECK (status IN ('succeeded', 'failed', 'skipped')),
    duration_ms         INTEGER NOT NULL,
    error               TEXT,
    provider_receipt_id TEXT,
    idempotency_key     TEXT    NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS notification_sends_tenant_idempotency_idx
    ON notification_sends (tenant_id, idempotency_key);
