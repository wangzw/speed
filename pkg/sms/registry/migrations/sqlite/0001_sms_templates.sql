CREATE TABLE IF NOT EXISTS sms_templates (
    name       TEXT      NOT NULL,
    params     TEXT      NOT NULL,
    codes      TEXT      NOT NULL,
    approvals  TEXT      NOT NULL,
    enabled    INTEGER   NOT NULL DEFAULT 1,
    version    INTEGER   NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (name)
);
