-- The in-app messages: one row per message in one account's inbox, with tenant_id leading the key as on every scoped table.
-- Every instant is milliseconds since the Unix epoch. expires_at is empty for a message that never expires; read_at holds the first-read instant.
-- group is a reserved word, so it is quoted. The unique index on dedupe_key makes a repeated write converge on the existing row, and the (tenant_id, recipient_user_id, created_at) index serves a recipient's listing.
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
