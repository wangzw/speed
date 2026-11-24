ALTER TABLE sms_templates ADD COLUMN carrier TEXT NOT NULL DEFAULT '';
ALTER TABLE sms_templates ADD COLUMN kind TEXT NOT NULL DEFAULT 'notification';
ALTER TABLE sms_templates ADD COLUMN bodies TEXT NOT NULL DEFAULT '{}';
ALTER TABLE sms_templates ADD COLUMN attributes TEXT NOT NULL DEFAULT '{}';
CREATE TABLE sms_templates_carrier (
    carrier    TEXT      NOT NULL,
    name       TEXT      NOT NULL,
    kind       TEXT      NOT NULL DEFAULT 'notification',
    params     TEXT      NOT NULL,
    codes      TEXT      NOT NULL,
    bodies     TEXT      NOT NULL DEFAULT '{}',
    attributes TEXT      NOT NULL DEFAULT '{}',
    approvals  TEXT      NOT NULL,
    enabled    INTEGER   NOT NULL DEFAULT 1,
    version    INTEGER   NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (carrier, name)
);
INSERT INTO sms_templates_carrier (carrier, name, kind, params, codes, bodies, attributes, approvals, enabled,
    version, created_at, updated_at)
    SELECT carrier, name, kind, params, codes, bodies, attributes, approvals, enabled, version, created_at, updated_at
    FROM sms_templates;
DROP TABLE sms_templates;
ALTER TABLE sms_templates_carrier RENAME TO sms_templates;

CREATE TABLE IF NOT EXISTS sms_carrier_ownership (
    singleton INTEGER NOT NULL CHECK (singleton = 1),
    carrier   TEXT    NOT NULL,
    PRIMARY KEY (singleton)
);
