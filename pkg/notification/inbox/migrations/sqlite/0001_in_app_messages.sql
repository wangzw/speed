-- The in-app messages: one row per message written into one account's inbox.
--
-- The tenant column leads the key, as it does on every scoped table: the model
-- carries tenant.Model, and the key the convention declares is the key the table
-- must declare. It is not null, so a row that belongs to no tenant cannot land
-- here.
--
-- Every instant is stored as milliseconds since the Unix epoch rather than as a
-- timestamp type, so a row written through the model path and a value planted by
-- hand carry the same number on either engine. expires_at is empty for a message
-- that never expires; read_at is empty until the first marking and holds that
-- first-read instant afterwards.
--
-- group is a reserved word, so it is quoted here and reached through GORM's
-- model path, which quotes it in turn. The unique index on dedupe_key is what
-- makes a repeated write of one key converge on the row already there; the dedupe
-- key already carries the tenant, so one global unique index is enough. The
-- (tenant_id, recipient_user_id, created_at) index serves a recipient's listing
-- in created_at order.
CREATE TABLE IF NOT EXISTS in_app_messages (
    tenant_id         TEXT    NOT NULL,
    id                TEXT    NOT NULL,
    recipient_user_id TEXT    NOT NULL,
    type_key          TEXT    NOT NULL,
    "group"           TEXT    NOT NULL,
    title             TEXT    NOT NULL,
    body              TEXT    NOT NULL,
    params            TEXT    NOT NULL,
    link              TEXT    NOT NULL,
    dedupe_key        TEXT    NOT NULL,
    expires_at        INTEGER,
    read_at           INTEGER,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS in_app_messages_dedupe_key_idx ON in_app_messages (dedupe_key);

CREATE INDEX IF NOT EXISTS in_app_messages_recipient_idx ON in_app_messages (tenant_id, recipient_user_id, created_at);
