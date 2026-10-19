# pkg/tenant/admin

The tenant capability's management module. It mounts the roster-management routes
under `/api/v1/system/tenants` and delivers no capability of its own: every route's
action is a call on the roster, reached through the `tenant.Catalog` capability,
and the module holds no tenant, no rule and no data of its own. It builds no
table, caches nothing and migrates nothing.

The descriptor declares the static feature `tenant.admin`, present whenever the
module is in force.

The roster owns which tenants exist — what names they carry, which are served —
so this module only decides who may ask and how the answer is shaped. It carries
no policy rule: it asks the decision of the handler-internal `authz.guard` API
and maps the roster's refusals to responses.

Taken in only by a command that wants the management routes, it is absent from
every other command: without it the roster is still read and written through the
`tenant.Catalog` Go interface, and the host's own commands carry their access
control.

The API is the package's godoc, plus the `Catalog`, `Tenant`, `ID` and `Status`
types and the sentinels in `pkg/tenant`.

## Routes and the permissions they are held to

Every route is mounted under the base path `/api/v1/system`, on the endpoints the
module's configuration lists. A route is addressed by a natural key — a tenant
identifier, the same key the roster keys a row by — so no opaque row identifier
ever appears in a path. The whole surface is one group, `tenants`, and it is
always mounted, because the roster it reads and writes is required. This set is
the module's whole route surface.

| Group | Method and path | Permission |
|---|---|---|
| Tenants | `GET /api/v1/system/tenants` | `tenant.read` |
| Tenants | `POST /api/v1/system/tenants` | `tenant.manage` |
| Tenants | `GET /api/v1/system/tenants/:id` | `tenant.read` |
| Tenants | `PATCH /api/v1/system/tenants/:id` | `tenant.manage` |
| Tenants | `POST /api/v1/system/tenants/:id/status` | `tenant.manage` |

A read route is held to `tenant.read` and a write route to `tenant.manage`. Each
route's action is one `tenant.Catalog` call:

| Route | Roster action |
|---|---|
| `GET /tenants` | `List`, every tenant, in the roster's order (by identifier) |
| `POST /tenants` | `Create`, a tenant not registered yet |
| `GET /tenants/:id` | `Get`, one tenant by identifier |
| `PATCH /tenants/:id` | `SetAlias`, a new alias |
| `POST /tenants/:id/status` | `SetStatus`, a new state |

A read answers 200 with a view; a write that creates a record answers 201 with the
record's view, and a write that lands without one (rename, status) answers 204 and
no body.

The two permissions are this module's own: the tenant capability names none, so
this module declares the vocabulary it decides its routes on. It carries
`authz.PermissionSpec` and `authz.TranslationSpec` resources — the two
permissions, and the `tenant`/`read` and `tenant`/`manage` pair each translates to
— so the process's catalog holds them and the process's translation expresses a
decision on either. Without the translation the guard could not express a
permission and every route would refuse its caller.

## Boundaries

- **Register goes through `Create`, and never overwrites.** Opening a tenant is an
  insert of an identifier the roster's table does not hold yet. An identifier it
  already holds is refused by `Create` with `tenant.ErrTenantExists`, answered a
  409; nothing is written. This is where `Create` and `Put` divide: `Put` rewrites
  the row its identifier names, alias and status together, which is a seed's
  action, while `Create` only inserts. A registration starts the tenant `active`;
  there is no delete, and disabling is the only way to retire one.
- **The rename goes through `SetAlias`, never `Put`.** `Put` takes the whole row's
  new values and writes the status back with the alias, so a rename through it
  would carry whatever status this process last read, and a tenant another replica
  disabled in the window between the snapshot and the table would be served again.
  `SetAlias` writes the alias column alone and reads no status.
- **One decision order, for every route, read and write alike.** The route's guard
  wrapper reads the caller first: a request with no identity is a 401 — the
  problem the admission and authorization layers write for a route that requires
  an identity and carries none: the status alone, no type, and the realm
  challenge naming the endpoint the route was mounted on — and the wrapper
  returns without asking the guard. Then it asks the guard for the route's
  permission: a shortfall is the guard's own refusal, a 403 for a denial and a
  503 for an outage. Only past the decision does a write parse its body, run its
  checks, land its audit and call the roster, or a read call the roster and
  answer. The wrapper sits in front of the declared handler because the entry
  point binds a typed operation's input before it calls the handler.
- **The decision is made in the platform domain, whatever tenant the request
  carries.** The roster is the process's own table of which tenants exist and not
  a tenant's, so the handler asks through the guard's explicit-domain form with
  `authz.PlatformDomain` and the request's tenant never steers it: deciding in the
  request's own tenant would let a caller authorized in one tenant reach the whole
  roster by sending a tenant header. Listing the roster is a platform read, not a
  cross-tenant one — the roster's table is not tenant-scoped and its reads are
  outside the filter — so it opens no cross-tenant context and needs no
  cross-tenant permission.
- **The mandatory audit of a write lands before the action, outside any
  transaction.** A write's audit row is written before the roster is called; a row
  that does not land is answered 503 and the action is not carried out, so a
  change that could not be recorded is not made. When the roster refuses the
  write, a failed row linked to the permitted one by the action identifier is
  written best-effort: its own failure is an Error line and changes nothing about
  the answer. The action names are a closed set — `tenant.create`, `tenant.update`,
  `tenant.status` — one per write route, and a read writes no audit row of its own.
- **The refusal mapping is closed and fail-closed.** An identifier the roster's
  table already holds is a 409; an alias another tenant holds is a 409; an
  identifier no row carries is a 404; any other failure is a 503 whose cause stays
  in the log and never reaches the caller. A 404 and a 503 carry no problem type —
  they say something is not there or a dependency could not answer and not which
  refusal it was — while a 409 carries the capability's type prefix
  `urn:speed:tenant:`. `tenant.ErrTenantDisabled` and `tenant.ErrNoTenantInContext`
  are not mapped: they are the resolution and scoping paths' sentinels, and no
  roster write reached from a management route answers them, so one that arrives
  here falls to the same 503 an unknown failure gets.
- **A shape refusal happens after the decision and before the action.** A
  registration or a rename whose identifier or alias is outside the identifier
  domain, a body the handler cannot read, and a status outside the closed set
  `active`/`disabled` are 400s and reach neither the roster nor the audit. The
  checks use the root package's `tenant.IsIdentifier`; this module re-judges no
  value, it only refuses one outside the role the root package defines. A read
  runs no shape check of its own: whether an identifier names a tenant is the
  roster's to answer, and one it does not hold is a 404.
- **The module decides its own routes in a guard wrapper and declares them
  open.** The middleware lets a management request through to the wrapper, which
  makes the decision in front of the typed handler; the module grants itself no
  permission. Where the assembly delivers the route-declaration surface, `Init`
  registers every management route open on it. With no surface the routes carry
  no declaration, and a guarded endpoint answers them as undeclared — which is
  why a command that mounts these routes should name the enforcement module too.
- **The module delivers no capability and registers `tenant.admin`.** It carries no
  product another module resolves; it is taken in only to mount the routes. It
  states `StateEnabled` and never `StateAuto`: whether the management routes exist
  is a decision of the command tree, not a conclusion a configuration infers. It
  is the framework-provided provider for the roster: the tenant capability itself
  mounts no management face, and a host that wants one writes this module into its
  command rather than hand-writing the same routes, decision and audit preamble.
- **It requires the roster, the handler-internal guard and the audit capability.**
  A command that brings this one in without the roster to act on, the guard to
  decide with, or the audit to record through fails the startup before any route
  is mounted, rather than answering every route with a refusal or a write that
  cannot land. The HTTP entry point is taken up in `Init` and is required — a
  command that mounts the management routes and has no entry point has nothing to
  mount on, which is `authz.ErrNoRouter`. The logger is the one optional
  requirement, and the route-declaration surface is taken up optionally in `Init`.
- **The answer is a view, built field by field.** Every route answers with a view
  assembled from a `tenant.Tenant` and never the record as it stands, so a field
  added to a record later does not reach a client by being there. The view carries
  the tenant's identifier, its alias, and its state spelled the way
  `tenant.Status` spells it — `active` or `disabled`.

## Configuration

- **`tenant.admin.endpoints`** (configuration, a list of strings): the endpoints
  the management routes are mounted on, by the names the entry point's own
  configuration gives them. It is required and has no default — a management
  surface mounted nowhere would silently drop every route — and it takes concrete
  names only, never `*`, because mounting the routes on every endpoint would put
  them on the public ones, where no gateway guards them. The item takes the
  primary source alone. A shape the module cannot run on — an empty list, an empty
  name, `*`, a name written twice — is `tenant.admin`'s `ErrInvalidConfig`; a name
  the entry point does not declare is `authz.ErrUnknownEndpoint` at startup.

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
go -C pkg build ./tenant/...
go -C pkg vet ./tenant/...
go -C pkg test ./tenant/admin/...
go -C pkg test -race ./tenant/admin/...
env -C pkg golangci-lint run ./tenant/admin/...
```

The tests ask for no service and no environment. The route cases mount the
module's whole route set on a real `gin` engine and drive it over an `httptest`
recorder, with stand-ins for the roster, the guard, the audit and the
configuration reader, and a real identity from `authntest` — so a case can assert
that a route the guard refused, a request with no identity, and a write whose
mandatory audit did not land reached no roster call and wrote no audit row. The
assembly cases drive the module's descriptor through a real registry, which is
where what it requires and takes up is observed; one of them drives the real
enforcement module alongside this one, which is what proves this module's
translation rows reach the process's one set without a conflict.
