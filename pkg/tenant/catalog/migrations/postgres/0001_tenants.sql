-- The roster: the tenants that exist, the name each is reached by, and
-- whether it is being served.
--
-- status is NOT NULL with a default of zero, and zero is "disabled". A row
-- whose status was never written is therefore not served, which is the
-- direction a value nobody chose has to fall.
CREATE TABLE tenants (
    id     TEXT    NOT NULL PRIMARY KEY,
    name   TEXT    NOT NULL UNIQUE,
    status INTEGER NOT NULL DEFAULT 0
);
