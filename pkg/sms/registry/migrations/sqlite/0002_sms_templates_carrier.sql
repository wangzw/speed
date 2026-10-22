-- The carrier dimension. One row per template one carrier holds: the carrier
-- column separates the carriers that share a database, and every read and write
-- filters by the carrier this process mounted.
--
-- kind is what the template is for. bodies is the language-to-body table and
-- attributes is the vendor registration identifiers, both JSON objects kept as
-- text. A row written before the carrier column existed carries the empty
-- carrier, which the first carrier to start against the database adopts under
-- the ownership marker below.
--
-- SQLite cannot change a primary key in place, so the table is rebuilt with the
-- new shape, its rows copied across, and the old table replaced.
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

-- The one-row ownership marker. It records which carrier adopted the legacy
-- rows of this database, and a later process mounting a different carrier fails
-- its startup against it rather than silently taking those rows over. The
-- singleton column and its check keep the table at one row.
CREATE TABLE IF NOT EXISTS sms_carrier_ownership (
    singleton INTEGER NOT NULL CHECK (singleton = 1),
    carrier   TEXT    NOT NULL,
    PRIMARY KEY (singleton)
);
