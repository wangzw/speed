-- The people at a tenant's accounts.
--
-- tenant_id leads the primary key, as on every scoped table (see the accounts
-- migration), and the reference to the account carries it as well: a contact
-- names an account of its own tenant, and a composite key is what makes the
-- database refuse one naming an identifier that only another tenant holds.
-- The contacts module declares a requirement on the accounts module's table
-- token, so this migration is applied after that one.
--
-- email and phone hold what the database module's encrypted serializer writes,
-- a base64 text longer than the plaintext, so both are TEXT. email_bidx is the
-- blind index of the normalised email: the one column an equality lookup by
-- email can match, filled by the contacts module on every write of the email.
--
-- The unique index is what decides whether an email is taken within a tenant.
-- It leads with the tenant, so the same person can be a contact of two tenants,
-- and it covers only the rows not soft-deleted, so a deleted contact's email is
-- free to be given to a new one.
CREATE TABLE contacts (
    tenant_id  TEXT        NOT NULL CHECK (tenant_id <> ''),
    id         TEXT        NOT NULL,
    account_id TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    email      TEXT        NOT NULL,
    email_bidx TEXT        NOT NULL CHECK (email_bidx <> ''),
    phone      TEXT,
    title      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id)
);

CREATE UNIQUE INDEX contacts_email_idx ON contacts (tenant_id, email_bidx) WHERE deleted_at IS NULL;

-- The listing by account and the listing's order, within one tenant.
CREATE INDEX contacts_account_idx ON contacts (tenant_id, account_id);
CREATE INDEX contacts_name_idx ON contacts (tenant_id, name, id);
