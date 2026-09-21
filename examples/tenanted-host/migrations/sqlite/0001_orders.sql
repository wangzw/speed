-- Orders, each row belonging to whichever tenant wrote it.
--
-- tenant_id is part of the primary key, and this is a correctness matter rather
-- than an indexing one. Keyed by the id alone, the insert a Save or an
-- OnConflict produces would conflict on another tenant's row and update that
-- row into the writer's tenant — a statement that names no other tenant, so
-- there is nothing for the filter to refuse. Leading every query with the
-- tenant column is the other half of it, and comes free here.
--
-- This key is the one the Go model declares: tenant.Model contributes
-- tenant_id as a primary key, and Order marks its own id beside it. A model and
-- a migration that disagree are noticed the first time something upserts.
CREATE TABLE orders (
    tenant_id TEXT NOT NULL,
    id        TEXT NOT NULL,
    item      TEXT NOT NULL,
    PRIMARY KEY (tenant_id, id)
);
