-- The organization profile: the name a tenant is shown by, a description, the
-- address the organization is reached at, and its website. An existing row
-- takes the empty string for each, and nothing is backfilled: a display name
-- this build invents would be a name nobody chose. The four columns are
-- NOT NULL because an empty string is the profile's absent value, not NULL.
--
-- created_at is NOT NULL with the registration's own moment. An existing
-- database is rebuilt rather than migrated, so there is no row without one and
-- no nullable compatibility to keep. The roster writes the real UTC instant on
-- every registration; the default is the fallback a row written around that
-- path takes, never a value a caller chooses.
ALTER TABLE tenants ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN contact_email TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN website TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;
