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
-- The primary key becomes (carrier, name), so one carrier holds at most one row
-- per template name while two carriers may each hold one under the same name.
ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS carrier TEXT NOT NULL DEFAULT '';
ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'notification';
ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS bodies TEXT NOT NULL DEFAULT '{}';
ALTER TABLE sms_templates ADD COLUMN IF NOT EXISTS attributes TEXT NOT NULL DEFAULT '{}';
ALTER TABLE sms_templates DROP CONSTRAINT IF EXISTS sms_templates_pkey;
ALTER TABLE sms_templates ADD PRIMARY KEY (carrier, name);

-- The one-row ownership marker. It records which carrier adopted the legacy
-- rows of this database, and a later process mounting a different carrier fails
-- its startup against it rather than silently taking those rows over. The
-- singleton column and its check keep the table at one row.
CREATE TABLE IF NOT EXISTS sms_carrier_ownership (
    singleton INTEGER NOT NULL CHECK (singleton = 1),
    carrier   TEXT    NOT NULL,
    PRIMARY KEY (singleton)
);
