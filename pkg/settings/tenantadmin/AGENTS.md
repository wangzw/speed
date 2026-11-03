# pkg/settings/tenantadmin

The settings capability's tenant management module. It mounts the tenant layer's
setting read and write routes under `/api/v1/tenants/:tenant/settings` and
delivers no capability of its own: every route's action is a call on the settings
surface, reached through the `settings.Store` capability, and the module holds no
setting, rule or data of its own.

The descriptor declares the static feature `settings.tenantadmin`, present
whenever the module is in force.

The route set is four routes under the tenant surface's base path:

- `GET /api/v1/tenants/:tenant/settings` — every declared item, minus the
  platform-only namespaces, with its value and source on that tenant's layer.
- `GET /api/v1/tenants/:tenant/settings/:namespace` — the same for one declared
  namespace; a namespace with no visible item is a bare 404.
- `PUT /api/v1/tenants/:tenant/settings/:namespace/:key` — one row written on
  that tenant's layer.
- `DELETE /api/v1/tenants/:tenant/settings/:namespace/:key` — that layer's
  explicit row dropped.

A listing view carries the declaration fields plus `written`, `has_row`,
`value` and `source`. `source` is the backend's answer to which layer the value
comes from — `tenant`, `global` or `default` — computed from the tenant layer's
rows and the global layer's rows; this surface does not expose the global layer,
so a client cannot work it out and must not try.

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
- **The global layer is not reachable here.** It is served by
  `pkg/settings/admin` under `/api/v1/system`; the path being the platform
  domain is answered with a bare 404.
- **The path tenant is reconciled with the resolved tenant by the tenant
  capability**, on the same layer the tenant management routes use. This module
  does not take the tenant roster in and does not re-implement that check.
- **A host supplies the mandatory `authz.WriteSettings` seam.** It refuses a
  write to a personal tenant. An assembly that mounts this module without one
  fails at dependency resolution rather than deciding the refusal at run time;
  the seam only narrows and never lets a write through. The set of
  platform-only namespaces is a different rule, held by this module's own closed
  set rather than by the seam.

The mandatory audit, the registration-state gate, the closed refusal mapping and
the sensitive-value masking follow the platform settings surface. A write lands
its mandatory audit row before the action and before any settings call, and an
audit row that cannot be written answers 503 with nothing changed. The host's
`authz.WriteSettings` seam is asked after the shape and platform-only checks and
before the audit; its refusal is 403 `urn:speed:settings:protected` and an
unreadable answer is 503, neither of which runs the action. A sensitive item's
text is masked in the view; its `has_row` and `source` still answer.

The platform-only namespaces are `ratelimit.*`, `tenant.admission.*` and
`authn.*`, held as a closed set in this package rather than in configuration or
derived from declarations. The list omits them and a single-key read or write
answers the same bare 404 an undeclared key gets. The exclusion narrows this
surface alone: the settings module and the platform surface still read and write
those namespaces.

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
permission and domain each route asks for, the platform-only exclusion, the
source marker, the masking, the registration gate, the host seam, the audit
ordering, the refusal mapping, the feature declaration, the endpoint defaults and
the generated fragment.
