# pkg/authz/admin

The authorization capability's management module. It mounts the permission
catalog, role, permission-grant and role-binding routes under `/authz/admin/*`
and delivers no capability of its own: every route's action is a table operation
of the policy module, reached through the `Manager` capability, and the module
holds no policy rule, no transaction and no data of its own.

The module grants itself no permission. It declares every management route open
on the authorization middleware — so the middleware lets a request through to
the handler — and the handler makes the decision itself: it reads the caller from
the request, asks the handler-internal `authz.guard` API for `authz.role.read` on
a read or `authz.role.manage` on a write, and answers a shortfall before anything
else runs. The module carries only the vocabulary the catalog already declares;
it puts no permission in place.

Taken in only by a command that wants the management routes, it is absent from
every other command: without it a tenant's roles, grants and bindings are still
read and written through the policy module's `Manager` Go interface, and the
host's own commands carry their access control.

The API is the package's godoc, plus the `Manager`, `Catalog`, `Role` and record
types in `pkg/authz`.

## Routes and the permissions they are held to

Every route is mounted under the base path `/authz/admin`, on the endpoints the
module's configuration lists. A route is addressed by a natural key — a tenant,
a role, a permission, an account — so no opaque row identifier ever appears in a
path. The tenant is taken from the path as the identifier of the rows the route
acts on — not an alias, and not the scope the decision is made in, which is the
platform domain (see "Boundaries"): this module holds no tenant roster and does
not look the tenant up. The groups are the catalog, roles, grants and bindings,
and every group is always mounted, because the manager and the catalog are
required. This set is the module's whole route surface.

| Group | Method and path | Permission |
|---|---|---|
| Catalog | `GET /authz/admin/permissions` | `authz.role.read` |
| Roles | `GET /authz/admin/tenants/:tenant/roles` | `authz.role.read` |
| Roles | `PUT /authz/admin/tenants/:tenant/roles/:role` | `authz.role.manage` |
| Roles | `DELETE /authz/admin/tenants/:tenant/roles/:role` | `authz.role.manage` |
| Grants | `GET /authz/admin/tenants/:tenant/roles/:role/grants` | `authz.role.read` |
| Grants | `PUT /authz/admin/tenants/:tenant/roles/:role/grants/:permission` | `authz.role.manage` |
| Grants | `DELETE /authz/admin/tenants/:tenant/roles/:role/grants/:permission` | `authz.role.manage` |
| Bindings | `GET /authz/admin/tenants/:tenant/bindings` | `authz.role.read` |
| Bindings | `GET /authz/admin/tenants/:tenant/bindings/:account` | `authz.role.read` |
| Bindings | `PUT /authz/admin/tenants/:tenant/bindings/:account/:role` | `authz.role.manage` |
| Bindings | `DELETE /authz/admin/tenants/:tenant/bindings/:account/:role` | `authz.role.manage` |

A read route is held to `authz.role.read` and a write route to
`authz.role.manage`; the catalog read is the one route whose manager call is the
`Catalog`'s `Declared`, not the `Manager`'s. A read answers 200 with a view; a
write that lands answers 204 and no body.

## Boundaries

- **One decision order, for every route, read and write alike.** The handler
  reads the caller first: a request with no identity is a 401 — the problem the
  admission and authorization layers write for a route that requires an identity
  and carries none: the status alone, no type, and the realm challenge naming the
  endpoint the route was mounted on — and the handler returns without asking the
  guard. Then it asks the guard for the route's permission: a shortfall is the
  guard's own refusal, a 403 for a denial and a 503 for an outage. Only past the
  decision does a write parse its body, run its checks, land its audit and call
  the `Manager`, or a read call the `Manager` and answer.
- **The decision is made in the platform domain, whatever tenant the path names.**
  The handler asks through the guard's explicit-domain form with
  `authz.PlatformDomain`, so the tenant in a management path is the data the
  route acts on and not the scope it is decided in. Deciding in the request's own
  tenant would let a caller authorized in one tenant read or write another's
  roles, grants and bindings by naming that tenant in the path. A caller without
  a grant in the platform domain is refused whatever tenant the path names.
  `TestTheDecisionIsAskedInThePlatformDomain`,
  `TestTheManagementDecisionIsMadeInThePlatformDomainNotTheResolvedTenant`.
- **The mandatory audit of a write lands before the action, outside any
  transaction.** A write's audit row is written before the `Manager` is called; a
  row that does not land is answered 503 and the action is not carried out, so a
  change that could not be recorded is not made. When the `Manager` refuses the
  write, a failed row linked to the permitted one by the action identifier is
  written best-effort: its own failure is an Error line and changes nothing about
  the answer. This module opens no transaction of its own; the row it changes is
  written in the `Manager`'s.
- **A grant is held to the catalog and translated before it is written.** A
  write that grants a permission the catalog does not hold, or a permission the
  translation has no row for, is a 400 before the `Manager` is touched: the
  module writes the (resource, action) pair the engine matches on, never the
  permission as a rule. A write that would delete a builtin role or change its
  permissions is a 409 — the target exists but cannot be changed — and the
  `Manager` judges that one.
- **The role write body carries the description key only, never the builtin
  mark.** The mark freezes a role for good — it cannot be rewritten, deleted or
  granted afterwards, and no path clears it — so the management surface does not
  set it: the host's seeding step does, through `Manager.MarkRoleBuiltin`. A
  `builtin` member a client sends reaches nothing; the handler forwards a record
  without it, and `PutRole` refuses a record that carries it.
- **The refusal mapping is closed and fail-closed.** An unknown role and a
  tenant mismatch — a request that named something that is not there, or crossed
  a tenant — are 400s; a builtin role is a 409; any other failure of the `Manager`
  or its store, and a mandatory audit that did not land, are 503s. A 503 says a
  dependency could not answer and never what was refused, so it carries no
  problem type. The cause of a 403, a 409 and a 503 stays in the log and never
  reaches the caller; the permission a route named never reaches the response.
- **The binding writes refuse an empty account on shape, after the decision and
  before the manager.** The route addresses a binding by its account, and the
  empty account names the zero subject, so a request that carries one is a 400
  and the manager is not called. An empty tenant is not refused here: the
  empty-tenant check is in the store, and the manager reports it as the
  tenant mismatch this module maps to a 400, so the write is forwarded to the
  manager with the empty tenant intact.
- **The module decides its own routes in the handler and declares them open.**
  The middleware lets a management request through to the handler, which makes
  the decision; the module grants itself no permission. Where the assembly
  delivers the route-declaration surface, `Init` registers every management route
  open on it. With no surface the routes carry no declaration, and a guarded
  endpoint answers them as undeclared — which is why a command that mounts these
  routes should name the enforcement module too.
- **The module delivers no capability and registers `authz.admin`.** It carries
  no product another module resolves; it is taken in only to mount the routes. It
  states `StateEnabled` and never `StateAuto`: whether the management routes exist
  is a decision of the command tree, not a conclusion a configuration infers.
- **It requires the policy manager, the catalog, the handler-internal guard and
  the audit capability, and each is another module's to deliver.** A command that
  brings this one in without the `Manager` to write through, the `Catalog` to hold
  a grant to, the guard to decide with, or the `Audit` to record through fails the
  startup before any route is mounted, rather than answering every route with a
  refusal or a write that cannot land. The HTTP entry point is taken up in `Init`
  and is required — a command that mounts the management routes and has no entry
  point has nothing to mount on, which is `authz.ErrNoRouter`. The logger is the
  one optional requirement, and the route-declaration surface is taken up
  optionally in `Init`.
- **The answer is a view, built field by field.** Every route answers with a view
  assembled from a root-package value and never the value as it stands, so a field
  added to a record later does not reach a client by being there.
- **A secret and a policy value reach no log line, no error text and no metric.**
  The audit detail names a tenant and a row's natural key — a role, a permission,
  an account — and never a credential or a policy secret; a grant's resource and
  action are named, never the values behind them.

## Configuration

- **`authz.admin.endpoints`** (configuration, a list of strings): the endpoints
  the management routes are mounted on, by the names the entry point's own
  configuration gives them. It is required and has no default — a management
  surface mounted nowhere would silently drop every route — and it takes concrete
  names only, never `*`, because mounting the routes on every endpoint would put
  them on the public ones, where no gateway guards them. The item takes the
  primary source alone. The module has no failure sentinel of its own, so a shape
  it cannot run on — an empty list, an empty name, `*`, a name written twice —
  is `authn.ErrInvalidConfig`; a name the entry point does not declare is
  `authz.ErrUnknownEndpoint` at startup.

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
go -C pkg build ./authz/...
go -C pkg vet ./authz/...
go -C pkg test ./authz/admin/...
go -C pkg test -race ./authz/admin/...
env -C pkg golangci-lint run ./authz/admin/...
```

The tests ask for no service and no environment. The cases drive the handlers
over a real `*gin.Context` on an `httptest` recorder, with stand-ins for the
manager, the catalog, the guard, the audit and the configuration reader, which
count and record the calls they got — so a case can assert that a route the guard
refused, and a write whose audit did not land, reached no table. The assembly
cases drive the module's descriptor through a real registry, which is where what
it requires and takes up is observed. A live case runs the real entry point and
the module's own routes end to end on a loopback port the system picks.
