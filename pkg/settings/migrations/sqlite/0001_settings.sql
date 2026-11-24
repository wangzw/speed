CREATE TABLE IF NOT EXISTS settings (
    tenant_id  TEXT      NOT NULL DEFAULT '',
    key        TEXT      NOT NULL,
    value      TEXT      NOT NULL,
    version    INTEGER   NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (tenant_id, key)
);
