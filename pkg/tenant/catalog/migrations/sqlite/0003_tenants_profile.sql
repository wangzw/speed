-- The organization profile: the name a tenant is shown by, a description, the
-- address the organization is reached at, and its website. An existing row
-- takes the empty string for each, and nothing is backfilled: a display name
-- this build invents would be a name nobody chose. The four columns are
-- NOT NULL because an empty string is the profile's absent value, not NULL.
--
-- created_at is NOT NULL with the registration's own moment. The roster writes
-- the real UTC instant on every registration; the default is the fallback a row
-- written around that path takes, never a value a caller chooses.
--
-- The table is rebuilt instead of altered in place. SQLite refuses an ADD
-- COLUMN whose default is not a constant once the table holds a row, and
-- CURRENT_TIMESTAMP is the default this column owes, so the five columns arrive
-- through a new table that carries every existing row. A carried row takes the
-- migration's instant for created_at, which is the fallback the column declares.
CREATE TABLE tenants_profile_rebuild (
    id            TEXT      NOT NULL PRIMARY KEY,
    name          TEXT      NOT NULL UNIQUE,
    status        INTEGER   NOT NULL DEFAULT 0,
    disabled_by   INTEGER   NOT NULL DEFAULT 0,
    display_name  TEXT      NOT NULL DEFAULT '',
    description   TEXT      NOT NULL DEFAULT '',
    contact_email TEXT      NOT NULL DEFAULT '',
    website       TEXT      NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO tenants_profile_rebuild (id, name, status, disabled_by)
    SELECT id, name, status, disabled_by FROM tenants;
DROP TABLE tenants;
ALTER TABLE tenants_profile_rebuild RENAME TO tenants;
