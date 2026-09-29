-- Which CRM user belongs to which tenant, and in what role.
--
-- tenant_id leads the primary key, and this is a correctness matter rather than
-- an indexing one. Keyed by the user alone, the insert a Save produces would
-- conflict on another tenant's row and update that row into the writer's
-- tenant — a statement that names no other tenant, so there is nothing for the
-- filter to refuse. The key is the one the Go model declares: tenant.Model
-- contributes tenant_id, and Membership marks user_id beside it.
--
-- The CHECK on tenant_id catches the one write the NOT NULL does not: a
-- statement issued where the tenant filter is not installed writes an empty
-- tenant rather than failing.
--
-- user_id names a row of the users table, which another module owns. The
-- reference needs the users migration applied first, and the order between two
-- modules' migrations comes only from a requirement one places on a capability
-- the other delivers: the members module declares one on the users module's
-- table token, which is what that token is for.
CREATE TABLE memberships (
    tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
    user_id    TEXT NOT NULL REFERENCES users (id),
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'rep', 'viewer')),
    granted_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, user_id)
);

-- The one index that does not lead with the tenant column. It serves the
-- account endpoint's question of which tenants a user belongs to, which is a
-- question across tenants by definition and is asked under an explicit
-- cross-tenant context; every scoped read goes through the primary key.
CREATE INDEX memberships_user_idx ON memberships (user_id, tenant_id);
