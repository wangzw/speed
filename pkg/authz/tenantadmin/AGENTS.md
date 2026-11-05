# pkg/authz/tenantadmin

The authorization capability's tenant management module. It mounts the
tenant-scoped permission catalog, role, grant and binding routes under
`/api/v1/tenants/:tenant/*` and delivers no capability of its own: every
route's action is a table operation of the policy module, reached through the
`Manager` and `Catalog` capabilities, and the module holds no policy rule,
transaction or data of its own.

The descriptor declares the static feature `authz.tenantadmin`, present whenever
the module is in force.

## What the module is for

This is the tenant-domain counterpart of `pkg/authz/admin`. Both read and write
the same policy tables through the same `Manager` and `Catalog`; the two differ
in the policy domain a route is decided in, in the permission codes they ask for
and in the anti-escalation rules they apply.

- **The decision is made in the tenant the path names**, not in the platform
  domain. `platform-admin` admits nothing here by itself, and a caller needs a
  binding in that tenant.
- **The permission codes are the tenant ones**: `authz.tenant.role.read` and
  `authz.tenant.role.manage`, carried by the authorization capability itself.
  This module declares no catalog entry and no translation row.
- **The catalog read lists only the governed set**, `Catalog.TenantGoverned()`;
  a permission outside it is not shown and cannot be written through the grant
  routes. It lists the set whole and marks each permission `grantable` from the
  caller's held set; an unreadable held set is a 503.
- **The role list reports usage**: the grant count, the active member count and
  whether the caller holds `authz.tenant.role.manage` (`can_manage`). The member
  count is measured from the host's optional `AccountIdentityReader`; without it
  the count is null, and a reader that cannot answer fails the list as a 503.
- **The role usage read** (`GET /roles/:role/bindings`) pages the role's
  bindings by account cursor and reports each account's display name and
  activity from the same optional reader. Without it the name is empty and the
  activity null; a reader that cannot answer is a 503.
- **The path tenant is reconciled with the resolved tenant by the tenant
  capability**, before any route runs. The resolving module delivers the
  identity rule as the `pkg/http` `RouteGuard` capability and the identity rule
  plus the platform-domain refusal as `TenantAdminRouteGuard`; this module takes
  up the latter and wraps every route with it at the mount, so a mount with no
  such guard is refused. A path segment that names no unique tenant, another
  tenant, or the platform domain is answered with a bare 404. The platform
  domain is refused here and on `settings.tenantadmin`, not on the shared guard
  the mounter applies to the members, invitations and SSO groups.
- **The module never holds a `Seeder` or a `Reconciler`.** Those are the host's
  seeding surfaces, and a builtin role's grant set is changed only by them; a
  route cannot reach either.

## Routes

| Method | Path | Permission |
|---|---|---|
| GET | `/api/v1/tenants/:tenant/permissions` | `authz.tenant.role.read` |
| GET | `/api/v1/tenants/:tenant/roles` | `authz.tenant.role.read` |
| GET | `/api/v1/tenants/:tenant/roles/:role/bindings` | `authz.tenant.role.read` |
| PUT | `/api/v1/tenants/:tenant/roles/:role` | `authz.tenant.role.manage` |
| DELETE | `/api/v1/tenants/:tenant/roles/:role` | `authz.tenant.role.manage` |
| GET | `/api/v1/tenants/:tenant/roles/:role/grants` | `authz.tenant.role.read` |
| PUT | `/api/v1/tenants/:tenant/roles/:role/grants/:permission` | `authz.tenant.role.manage` |
| DELETE | `/api/v1/tenants/:tenant/roles/:role/grants/:permission` | `authz.tenant.role.manage` |
| GET | `/api/v1/tenants/:tenant/bindings` | `authz.tenant.role.read` |
| GET | `/api/v1/tenants/:tenant/bindings/:account` | `authz.tenant.role.read` |
| PUT | `/api/v1/tenants/:tenant/bindings/:account/:role` | `authz.tenant.role.manage` |
| DELETE | `/api/v1/tenants/:tenant/bindings/:account/:role` | `authz.tenant.role.manage` |

Every route is declared open on the authorization middleware; the guard wrapper
makes the decision in the path tenant's domain and answers a shortfall itself.
A read answers 200 with a view; a write answers 204 with no body.

## Anti-escalation

Each rule is a named check before the host write guard, the mandatory audit and
the manager; a refusal writes no `success` row and reaches no table.

- `platform-admin` is refused unconditionally by role name: no create, rewrite,
  delete, grant or binding of it (403 `protected`).
- The `tenant-admin` role row and its grants cannot be created, rewritten or
  deleted (403 `protected`). Binding `tenant-admin` to another account is
  allowed, subject to the rules below.
- A grant write names only a governed permission (400 `bad-request`).
- A caller may not grant a permission it does not hold (403 `protected`); an
  unreadable held set is 503.
- A binding may not hand out a role holding a permission the caller does not
  hold (403 `protected`).
- A binding account must be an active member of the tenant (400
  `bad-request`; a checker that cannot answer is 503).
- The last tenant-administrator binding cannot be removed (409 `conflict`).
- A caller cannot remove its own `tenant-admin` binding (403 `protected`).
- The host's optional `WriteGuard` is asked after the checks; a denial is 403
  `protected` and an unreadable answer is 503. This is the seam a personal
  tenant is refused by; this module does not decide that a tenant is personal.

## Audit

Every write lands one mandatory audit row before the action, outside any
transaction; if it cannot be written the write is a 503 and does not happen. A
write the manager refuses is closed with a `failed` row linked by `action_id`.
The action names are `authz.tenant.role.put`, `authz.tenant.role.delete`,
`authz.tenant.grant.put`, `authz.tenant.grant.delete`,
`authz.tenant.binding.put` and `authz.tenant.binding.delete`.

## Registration gate

A write from an account whose self-registration has not proved an email address
is a 403 `contact-required`; an unreadable state is 503. Service accounts and
accounts with no email address are exempt, and reads do not consult the state.

## Configuration

- **`authz.tenantadmin.endpoints`** (configuration, a list of strings): the
  endpoints the tenant management routes are mounted on, by the names the entry
  point's own configuration gives them. It defaults to `["api"]`, the endpoint
  tenant resolution guards in the assembled product. It holds concrete names
  only, never `*`. A name written twice, an empty name and `*` are
  `authn.ErrInvalidConfig`; a name the entry point does not declare is
  `authz.ErrUnknownEndpoint` at startup.

The module requires `Manager`, `Catalog`, `SubjectPermissions`,
`MembershipChecker`, `guard.Guard`, `audit.Audit` and
`authn.SignupVerification`. It takes up the entry point, the
`TenantAdminRouteGuard`, the route-declaration surface, the host `WriteGuard`, the message
localizer, the optional `AccountIdentityReader` and the logger in `Init`.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this module alone:

```
go -C pkg build ./authz/tenantadmin/...
go -C pkg vet ./authz/tenantadmin/...
go -C pkg test ./authz/tenantadmin/...
env -C pkg golangci-lint run ./authz/tenantadmin/...
```

The tests ask for no service and no environment. They hold the route table, the
decision's domain and permissions, each anti-escalation rule, the registration
gate, the host write guard, the mandatory audit, the closed failure mapping, the
feature declaration, the endpoint default and the generated OpenAPI fragment.
