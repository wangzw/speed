-- A role carries a free-text description beside the i18n key a host seeds into a
-- builtin role. The key stays the builtin role's localisable text, while the
-- description is what the management surface writes for a role it created, and
-- an existing row reads back the empty string rather than a broken one.
--
-- The new index is the role-usage page's read: one role's bindings of a tenant,
-- ordered by account, which the cursor page turns into a range scan.
ALTER TABLE authz_roles ADD COLUMN description TEXT NOT NULL DEFAULT '';

CREATE INDEX authz_bindings_by_role_account ON authz_bindings (tenant_id, role, account_id);
