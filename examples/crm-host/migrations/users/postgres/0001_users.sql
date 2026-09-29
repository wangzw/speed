-- CRM users. The table is global rather than scoped: a user exists once and
-- belongs to tenants through memberships, and the credential check that reads
-- it runs before any tenant has been resolved.
--
-- The email is stored normalised by the module that writes it, so the unique
-- constraint is the constraint on the address a person types rather than on a
-- spelling of it.
CREATE TABLE users (
    id            TEXT        NOT NULL PRIMARY KEY,
    email         TEXT        NOT NULL UNIQUE,
    display_name  TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    disabled      BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL
);
