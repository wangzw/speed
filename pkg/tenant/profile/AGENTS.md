# pkg/tenant/profile

The tenant capability's organization-profile module: it mounts
`GET` and `PATCH /api/v1/tenants/{tenant}/profile` and delivers no capability
of its own.

## Boundaries

- **This module holds no table and no rule of its own.** Every read and write is
  a call on `tenant.Catalog`; the profile's bounds and its `Normalize`/`Validate`
  live in the root package, and this surface calls them rather than restating
  them.
- **The routes are tenant-scoped and must be mounted with the tenant
  administration guard.** `pkg/tenant/resolve` delivers
  `speedhttp.TenantAdminRouteGuard`; the mount wraps every route with it and
  refuses an assembly that delivers none. The guard holds the path tenant to the
  tenant the request resolved to, so a caller authorized in one tenant cannot
  reach another's profile by naming it, and it refuses the platform domain.
- **The permission decision is made in the resolved tenant's identifier.** The
  path may carry an alias; the policy domain never does. `tenant.profile.read`
  and `tenant.profile.write` are declared in the central catalog
  (`pkg/authz/policy`) and translated in `pkg/authz/internal/translation`; this
  module declares no permission spec of its own. Both codes are in the governed
  set, so a tenant administrator holds them by default. A personal tenant's
  tenant-admin allowlist is the host's explicit business list and does not
  include them.
- **A write requires a proved email address before the permission decision.**
  The gate reads the request's actor, not its impersonation target, and a state
  that cannot be read is a 503, never a pass.
- **The mandatory audit row lands before `SetProfile`.** The action is
  `tenant.profile.update`; the detail carries `action_id`, `tenant` and
  `surface: tenant`. A roster refusal closes the write with a linked failed row.
- **The answer is not the platform view.** `TenantProfileView` carries the
  identifier, alias, the four descriptive fields, state and, when known, the
  creation time. It deliberately omits the disable source, which belongs to the
  platform management surface.
- **The mount is configuration-driven.** The endpoints come from
  `tenant.profile.endpoints`, defaulting to `["api"]`; the wildcard, blank name
  and duplicate are refused at startup. The module declares the static feature
  `tenant.profile`, which the frontend gates the page and navigation by.

## Running it

From the repository root:

```
go -C pkg test ./tenant/profile/...
go -C pkg vet ./tenant/profile/...
```

The OpenAPI fragment is generated from the operation declarations. Regenerate it
with:

```
UPDATE_FRAGMENTS=1 go -C pkg test ./tenant/profile -run TestTheFragmentIsGeneratedFromTheDeclarations
```

`handlers_test.go` pins the read, the write, the email gate, the audit order and
the failure mapping; `guard_test.go` pins the cross-tenant and platform-domain
refusals against the real path guard; `module_test.go` pins the assembly, route
mounting and configuration; `openapi_generated_test.go` holds the fragment to
the declarations.
