-- The role an account has inside a tenant.
--
-- Which accounts belong to a tenant is the account module's to say, through
-- authn_memberships; this table holds only the role the tenant gives one of
-- them, so a member with no row here has no role. It names no foreign key: a
-- cross-module reference would couple this migration's order to the account
-- module's, and the membership relation is checked through the interface, not by
-- the database.
--
-- tenant_id leads the primary key, and this is a correctness matter rather than
-- an indexing one. Keyed by the account alone, the insert a Save produces would
-- conflict on another tenant's row and update that row into the writer's
-- tenant — a statement that names no other tenant, so there is nothing for the
-- filter to refuse. The key is the one the Go model declares: tenant.Model
-- contributes tenant_id, and MemberRole marks account_id beside it.
--
-- The CHECK on tenant_id catches the one write the NOT NULL does not: a
-- statement issued where the tenant filter is not installed writes an empty
-- tenant rather than failing.
CREATE TABLE member_roles (
    tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
    account_id TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'rep', 'viewer')),
    granted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, account_id)
);
