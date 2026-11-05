-- The organization profile: the name a tenant is shown by, a description, the
-- address the organization is reached at, and its website. An existing row
-- takes the empty string for each, and nothing is backfilled: a display name
-- this build invents would be a name nobody chose. The four columns are
-- NOT NULL because an empty string is the profile's absent value, not NULL.
--
-- created_at is nullable on purpose. A row that predates the column has no
-- creation time this build witnessed, and the roster leaves it NULL rather
-- than writing a zero time the request face would render as a real one. New
-- rows are written with the moment of their creation in UTC.
ALTER TABLE tenants ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN contact_email TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN website TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN created_at TIMESTAMPTZ NULL;
