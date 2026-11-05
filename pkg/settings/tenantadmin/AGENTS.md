# pkg/settings/tenantadmin

The settings capability's tenant management module. It mounts the tenant layer's
setting read and write routes under `/api/v1/tenants/:tenant/settings` and
delivers no capability of its own: every route's action is a call on the settings
surface, reached through the `settings.Store` capability, and the module holds no
setting, rule or data of its own.

The descriptor declares the static feature `settings.tenantadmin`, present
whenever the module is in force.

The route set is three routes under the tenant surface's base path:

- `GET /api/v1/tenants/:tenant/settings` — every item whose declaration exposes
  it to a tenant, each with its platform value, tenant value and effective
  value, in an envelope that also says whether the tenant's settings are
  writable.
- `PUT /api/v1/tenants/:tenant/settings/:namespace/:key` — one row written on
  that tenant's layer.
- `DELETE /api/v1/tenants/:tenant/settings/:namespace/:key` — that layer's
  explicit row dropped.

There is no per-namespace read. An unknown path is answered by the router with
404 or 405 before any setting is looked at, and the answer carries no key name,
value, source, default or row existence.

A listing item carries `key`, `title`, `description`, `type`, `control`,
`danger`, `effect`, `tenant_access`, `effective_value`, `platform_value`,
`tenant_value`, `locked_by_platform`, `has_row` and `source`. The three values
are text in the declared type's form. `platform_value` is the global row or the
declared default; `tenant_value` is the tenant row or the declared default;
`effective_value` is their boolean OR. `source` is `tenant`, `global` or
`default`, and a platform-enforced item is `global`. The envelope's `writable`
is the host seam's answer for the tenant, and `read_only_reason` is the refusal
code (`protected`) when it is false. Titles and descriptions render in the
request's language, falling back to the declared English text when no localizer
is assembled.

## What the module is for

This is the tenant-domain counterpart of `pkg/settings/admin`. Both read and
write through the same `settings.Store` and share the setting view; they differ
in the layer a route acts on and in the permission it asks for.

- **The layer is fixed by the tenant in the path**:
  `settings.ForTenant(tenant)`. There is no `layer` or `tenant` query parameter:
  one appearing on this surface is a bad request, so no request can point a
  write at the global layer or at another tenant's.
- **The decision is made in that tenant's policy domain**, with
  `settings.tenant.read` on a read and `settings.tenant.write` on a write. The
  two codes belong to the governed set and are carried by the authorization
  capability; this module declares no catalog entry and no translation row.
- **The global layer is not writable here.** It is served by
  `pkg/settings/admin` under `/api/v1/system`; the path being the platform
  domain is answered with a bare 404. The listing reads the global layer's rows
  only to compute the platform value and the effective value of the items the
  declarations already expose, and it never lists the global layer itself.
- **The path tenant is reconciled with the resolved tenant by the tenant
  capability**, on the same layer the tenant management routes use. This module
  does not take the tenant roster in and does not re-implement that check.
- **A host supplies the mandatory `authz.WriteSettings` seam.** It refuses a
  write to a personal tenant. An assembly that mounts this module without one
  fails at dependency resolution rather than deciding the refusal at run time;
  the seam only narrows and never lets a write through.

The mandatory audit, the registration-state gate, the closed refusal mapping and
the sensitive-value masking follow the platform settings surface. A write lands
its mandatory audit row before the action and before any settings call, and an
audit row that cannot be written answers 503 with nothing changed. The host's
`authz.WriteSettings` seam is asked after the declaration and platform-enforcement
checks and before the audit; its refusal is 403 `urn:speed:settings:protected`
and an unreadable answer is 503, neither of which runs the action. A sensitive
item's three value fields carry the settings capability's mask; its `has_row`
and `source` still answer. A write that carries the mask string is 422
`urn:speed:settings:masked-value` before the seam, the audit and the store.

## What a tenant sees

The declarations decide the surface. A schema states `Schema.TenantFacing`, and
each item states a `TenantAccess`. An item whose declaration gives it no tenant
access is invisible here; a namespace's name neither opens nor closes anything,
and there is no prefix list. The schema-level switch is judged when the key
table is built, so an item that states a tenant access without a tenant-facing
schema fails the startup.

Only `TenantReadWrite` is writable. `TenantRead` is visible and unwritable; both
write routes answer the same bare 404 an undeclared key gets. An item the
declarations do not expose is invisible in the listing, absent from every error
and from the OpenAPI fragment, and gets that same 404 on a write.

A platform-enforced policy is one whose global layer holds the boolean `true`.
The effective value is the platform value OR the tenant value, so the platform
layer is a floor. A write that would turn such a policy off answers 409
`urn:speed:settings:tenant-admission-enforced` before the seam, the audit and the
store; the tenant layer is unchanged and no success audit lands. Writing the
enforced value and deleting the tenant row remain legal.

## Configuration

- **`settings.tenantadmin.endpoints`** (configuration, a list of strings): the
  endpoints the tenant settings routes are mounted on, by the names the entry
  point's own configuration gives them. It defaults to `["api"]`, the endpoint
  tenant resolution guards in the assembled product. It holds concrete names
  only, never `*`: mounting the routes on every endpoint would put them where no
  tenant resolution or tenant policy guard reaches. A name written twice, an
  empty name and `*` are `ErrInvalidConfig`; a name the entry point does not
  declare is `authz.ErrUnknownEndpoint` at startup.

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
go -C pkg build ./settings/tenantadmin/...
go -C pkg vet ./settings/tenantadmin/...
go -C pkg test ./settings/tenantadmin/...
env -C pkg golangci-lint run ./settings/tenantadmin/...
```

The tests ask for no service and no environment. They hold the route set, the
permission and domain each route asks for, the declaration-driven visibility, the
effective value and its source, the platform-enforcement refusal, the envelope,
the localized titles, the masking and the masked-write refusal, the registration
gate, the host seam, the audit ordering, the refusal mapping, the feature
declaration, the endpoint defaults and the generated fragment.
