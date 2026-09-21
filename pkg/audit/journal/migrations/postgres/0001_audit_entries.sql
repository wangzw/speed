CREATE TABLE IF NOT EXISTS audit_entries (
    id          BIGSERIAL   PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    actor       TEXT        NOT NULL,
    tenant_id   TEXT,
    source      TEXT        NOT NULL,
    action      TEXT        NOT NULL,
    object      TEXT        NOT NULL,
    outcome     TEXT        NOT NULL,
    purpose     TEXT        NOT NULL,
    detail      TEXT        NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_entries_occurred_at_idx ON audit_entries (occurred_at);
CREATE INDEX IF NOT EXISTS audit_entries_tenant_id_idx ON audit_entries (tenant_id);
