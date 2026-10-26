-- Tenant administration: the platform administrator role lives only in the
-- platform domain.
--
-- Before the system tenant existed as a roster row, every tenant domain carried
-- its own copy of platform-admin and bound that tenant's members to it, so a
-- business tenant's member held every tenant-level permission through a role
-- whose name said platform. The role now exists in the platform domain alone,
-- and a binding to it requires an active membership of that tenant. An upgraded
-- database therefore has to withdraw the copies outside the platform domain,
-- with their grants and bindings.
--
-- The deletes name the rows they remove, so the migration is idempotent: a
-- second application removes nothing and leaves every surviving row as it was. A
-- fresh database holds no policy rows here and the migration is a no-op. The
-- tenant-administrator role belongs to every domain and is not touched.
DELETE FROM authz_bindings WHERE role = 'platform-admin' AND tenant_id <> 'system';
DELETE FROM authz_grants WHERE role = 'platform-admin' AND tenant_id <> 'system';
DELETE FROM authz_roles WHERE role = 'platform-admin' AND tenant_id <> 'system';
