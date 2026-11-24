CREATE TABLE IF NOT EXISTS sms_templates (
    name       TEXT        NOT NULL,
    params     TEXT        NOT NULL,
    codes      TEXT        NOT NULL,
    approvals  TEXT        NOT NULL,
    enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    version    BIGINT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (name)
);
