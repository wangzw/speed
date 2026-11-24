ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS carrier TEXT NOT NULL DEFAULT '';
ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'notification';
ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS bodies TEXT NOT NULL DEFAULT '{}';
ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS attributes TEXT NOT NULL DEFAULT '{}';
ALTER TABLE sms_templates DROP CONSTRAINT IF EXISTS sms_templates_pkey;
ALTER TABLE sms_templates ADD PRIMARY KEY (carrier, name);

CREATE TABLE IF NOT EXISTS sms_carrier_ownership (
    singleton INTEGER NOT NULL CHECK (singleton = 1),
    carrier   TEXT    NOT NULL,
    PRIMARY KEY (singleton)
);
