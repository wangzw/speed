# pkg/authz

The authorization capability directory: deciding whether an identified subject
may do a thing on a resource. The interface package a consumer of a permission
imports; `pkg/authz/policy` — the module that owns the policy tables and their
migrations, the thin store the engine reads and writes through, the permission
catalog, the engine snapshot the decisions read, the manager that reads and
writes the policy data, the host's seeding and reconciliation surfaces, and the
account lifecycle cleanup — `pkg/authz/guard`, the module that implements the
authentication capability's decision seam, `pkg/authz/admin`, the platform
management surface, `pkg/authz/tenantadmin`, the tenant-scoped management
surface (a skeleton that mounts no route yet), `pkg/authz/self`, the module that
answers the caller's own permissions, and `pkg/authz/authztest`, the fixtures a
test of this directory drives. The
thin adapter the engine stores its policy through, the snapshot holder, and the
one permission translation live under `pkg/authz/internal/`, which only this
directory can import.

The platform management surface is `authz.admin`: it mounts its routes under
`/api/v1/system/*` and delivers nothing of its own. The tenant management surface
is `authz.tenantadmin`, a skeleton that mounts no route yet: when its handlers
land it mounts its routes under `/api/v1/tenants/{tenant}/*`, decides in the
tenant's own policy domain,
and delivers nothing of its own.
`authz.self` mounts the caller's permission read under
`/api/v1/authz/me/permissions` and delivers nothing of its own either.

The permission set has two readings. `Catalog.Declared` is every permission any
registered module declares. `Catalog.TenantGoverned` is the subset a tenant
administrator may hold and grant: a permission with a tenant-scoped endpoint,
defined by the platform as a closed set. A module marks its own governed
permissions in `PermissionSpec.TenantGoverned`, which must be a subset of
`PermissionSpec.Permissions`. `platform-admin` exists only in the platform domain
and is granted `Declared`; on a tenant surface it admits nothing by itself, and a
caller needs a binding in that tenant. `tenant-admin` has two tiers: in a
business or system tenant it is granted `TenantGoverned`; in a personal tenant it
is granted the host's business-resource set intersected with `TenantGoverned`,
minus the host's excluded set; it is not seeded as a builtin and is never
converged by `Reconciler`. `Governed` answers whether one declared permission is
in the governed set.

The API is the package's godoc. Read the capability interfaces' comments
before writing a consumer.

The root package also defines `TenantLock`, the per-tenant lock that decides the
tenant-administrator floor: `Manager.DeleteBinding` and every
`MembershipGuard.Lock` take one object, so a binding delete and a membership
removal exclude each other. `InProcessTenantLock` is the default an assembly
gets when the host supplies none and serialises one process; a multi-replica
deployment supplies a database lock, which costs one pooled connection per held
lock. The lock is not reentrant: the membership-removal cascade and the
account-deletion cleanup clear bindings through `Manager.ClearAccountBindings`,
which takes no lock.

`TenantOwnerCounter` is the seam the account's deletion and suspension paths
ask beside the administrator floor: `OwnsActiveBusinessTenant` reads the
roster's owner column and names the active business tenant an account owns.
The policy subpackage delivers it from the same roster and write-guard reads
`TenantAdminCounter` uses. The two are independent: the floor counts
administrator bindings, the owner seam reads ownership, and the deletion asks
the owner first so an owner who is also the only administrator gets the
refusal that names the actual obstacle. `TenantOwnerCounter` is optional where
the account module takes it up, and an error is unavailable, never permission.

`TenantAliasResolver` is the id-to-alias seam the management views and audit
rows take up; the roster implements it, and this package declares it because the
authorization management module imports no tenant package. `TenantAdminCounter`
is the root package's other seam over the floor, and the
one the account-deletion path asks: `SoleTenantAdminTenant` names the active
business tenant whose only tenant-administrator binding an account holds, and
`LockAccountAdministration` takes the `TenantLock` of every tenant the account
administers so the deletion's check and cleanup cannot interleave with another
removal that judges one of those tenants' floors. The policy subpackage
delivers both beside `Manager`; it reads bindings across tenants, asks the
roster which tenants are in service, and asks the host's `WriteGuard` which
domains are not business tenants rather than inferring a kind from an
identifier. The seam is optional where the account module takes it up: an
assembly without it deletes accounts as before, and one whose counter or lock
cannot answer refuses the deletion instead of passing it. A host that supplies
its own counter implements the two methods together; the account module asks
the one interface, so a counter without the lock is not a valid
implementation.

## Modules and namespaces

- **The root package `pkg/authz` registers no module and exports no
  `ModuleName`.** It is imported by every consumer of a permission — the
  enforcement point, the management surface and any host code that names a
  permission — so a module registered here would put the policy engine into the
  available set of every binary that touches any of them and occupy a module
  name.
- **`pkg/authz/policy` registers `authz.policy`; `pkg/authz/guard` registers
  `authz.guard`; `pkg/authz/admin` registers `authz.admin`; `pkg/authz/tenantadmin`
  registers `authz.tenantadmin` (a skeleton that mounts no route yet); `pkg/authz/self` registers
  `authz.self`.** Each registering package exports its own `ModuleName`, and that
  same value is its configuration namespace, its static feature name and its
  OpenAPI tag; every segment of the name is a lower-case letter, a digit or a
  dash. Each of the packages that exists carries its own `AGENTS.md` beside it,
  stating that package's boundaries.
- **`pkg/authz/internal/persist`, `pkg/authz/internal/policy` and
  `pkg/authz/authztest` register no module.** The two `internal` packages are
  the directory's library units and `authztest` is a test fixture.
- **Namespaces do not overlap, and the root package owns none.** Two modules
  under this directory may not declare one input-item path, and no module's path
  may be a leaf of another's interior; `authz` itself is no module's leaf name.
- **What the namespaces hold today.** `authz.policy` declares the configuration
  items `authz.policy.load-timeout` and `authz.policy.refresh-interval`. The
  enforcement module's namespace is `authz.guard`; it declares the input items
  `authz.guard.endpoints` and `authz.guard.undeclared`. `authz.admin` declares
  `authz.admin.endpoints`; `authz.self` declares `authz.self.endpoints`.
  `authz.tenantadmin` is a skeleton that mounts no route yet; its namespace is
  `authz.tenantadmin`, its endpoint list declares
  `authz.tenantadmin.endpoints` and defaults to `["api"]`. Each list names
  concrete endpoints and refuses `*`; the tenant management module's default
  supplies its list, and the others are declared required.

## Boundaries

- **The root package imports the standard library alone.** A third-party
  library reached from here would enter every consumer's closure; the Casbin
  engine and GORM live in the policy subpackage, and gin may live only in a
  subpackage that speaks HTTP, never here. `deps_test.go` holds the root
  package's non-test imports to the standard library and holds every package of
  the module out of `authztest` in a file that is not a test.
- **The dependency direction is fixed.** This directory imports the
  authentication module's root package and delivers its decision seam;
  authentication imports nothing of this one. It reads the tenancy module's root
  package — the scoped model and the cross-tenant mark — and imports none of that
  module's subpackages. The admission layer's ordering anchor is named, not
  called: an authorization layer that has to sit inside admission points at it
  in `After`.
- **The model text is fixed in the root package.** `ModelText` is the one model
  the engine is loaded with; a host writes policy data and never the model, so
  the judgment language cannot be changed out from under the engine. A wrong
  matcher does not fail to load — it silently allows or denies where the model
  intends the opposite — so `policy` runs the self-check fixtures in
  `pkg/authz/internal/policy` against the root package's `ModelText`, the exact
  text the process will enforce with, before it begins serving, and a failure
  stops the startup.
- **Describing a policy row is the adapter's, and its construction is the whole
  context it gets.** The thin adapter's methods carry neither a context nor a
  tenant, so `pkg/authz/internal/persist` binds both once in `New`: the context a
  load runs under — marked cross-tenant with `tenant.CrossTenant`, so a load
  reads every tenant's rows, which is what the deciding process holds — and the
  caller a write is checked against, the empty identifier for a reader. A write
  whose rule names another tenant is refused with `authz.ErrTenantMismatch`;
  `SavePolicy` returns the adapter's own `ErrNotImplemented`, because the policy
  is changed one row at a time and a clear-and-rewrite would drop a write that
  landed between its read and its write. A module that builds its own session
  around the adapter, or that writes policy rows without it, is the shape to
  reject.
- **The seeding writes are capabilities of their own.** `Manager` refuses every
  write on a builtin role. `Seeder` adds grants to one and never removes,
  rewrites or deletes a grant; it is the only add-only path. `Reconciler`
  converges one domain's builtin `tenant-admin` to a target grant set and removes
  only the grants the caller lists as retired: it adds what the target holds and
  the role does not, removes the intersection of the role's grants with the
  retired set, returns the counts, and leaves any other grant in place. When the
  target set and the retired set overlap, the target set wins and a grant listed
  in both is kept. It never infers a removal from the catalog, so two replicas on
  different versions do not delete each other's grants. Both methods take grants
  as `[]Grant`: `SeedGrants` takes one set, `ReconcileGrants` takes the target and
  the retired set. Each call runs in one transaction; a failure rolls back every
  row of that call and a retry reconverges. After the commit the module reloads
  the snapshot and publishes `authz.policy.changed`, and both are idempotent. The
  error order is fixed: a role other than `tenant-admin` is `ErrReservedRole`
  before any read, an absent `tenant-admin` is `ErrUnknownRole`, and a
  `tenant-admin` that is not builtin is `ErrReservedRole`; all three precede any
  convergence write. A personal tenant's `tenant-admin` is not builtin, so
  `Reconciler` refuses it. The host's seeding step writes the mandatory audit row
  before it calls `ReconcileGrants`; an audit that cannot be written means no
  reconciliation.
  `authz.policy` delivers both exclusively, and neither `authz.admin` nor
  `authz.tenantadmin` (when implemented) takes either up — the wall between a
  management surface and a builtin role's grant set is the capabilities the
  module holds, not a branch in a handler. The host's seeding step is
  `Reconciler`'s only caller.
- **The governed set is a second, closed reading of the catalog.** A module
  declares the permissions it owns in `PermissionSpec.Permissions` and the
  tenant-governed subset in `PermissionSpec.TenantGoverned`, which must be a
  subset of the first or the startup fails with `ErrUnknownPermission`.
  `Catalog.TenantGoverned` unions the registered modules' marks; `Governed`
  answers for one permission. The platform's governed set is defined centrally
  in `policy`'s declaration, so a missed mark there removes a permission from
  every business or system tenant administrator. `platform-admin` exists only in
  the platform domain and is granted `Declared`; on a tenant surface it admits
  nothing by itself. A business or system tenant's `tenant-admin` is granted
  `TenantGoverned`; a personal tenant's is granted the host's business-resource
  set intersected with `TenantGoverned`, minus the host's excluded set; it is not
  seeded as a builtin and is never converged by the `Reconciler`. The split
  permission codes
  are `authz.role.read`/`authz.role.manage` and `settings.read`/`settings.write`
  for the platform surface, and `authz.tenant.role.read`/
  `authz.tenant.role.manage` and `settings.tenant.read`/`settings.tenant.write`
  for the tenant surface; the two pairs translate to different (object, action)
  pairs.
- **`authz.tenantadmin` is a skeleton that mounts no route yet.** When its
  handlers land it decides in the tenant domain and enforces the anti-escalation
  rules itself. It takes
  `Manager`, `Catalog`, `SubjectPermissions`, `MembershipChecker`, `guard.Guard`,
  `audit.Audit` and
  `authn.SignupVerification`, and only `Manager` of the policy writers. It
  declares its routes open, reads the caller and the signup state itself, and
  decides with `RequirePermissionIn` in the path tenant's domain. It refuses a
  `platform-admin` role or grant, refuses to create, rewrite or delete the
  `tenant-admin` role row and its grants, refuses a grant outside
  `TenantGoverned` or outside the caller's own held set, refuses a binding to a
  role carrying more than the caller holds, and refuses the platform domain and
  a path tenant that differs from the resolved tenant. Binding `tenant-admin`
  itself is allowed, subject to the subset, last-administrator and
  no-self-removal rules. The host mounts a path-tenant-equals-resolved-tenant
  middleware on every `ScopeTenants` route group; the module assumes it and does
  not re-implement it.
- **The management surfaces' write guards are host seams, not capabilities.**
  `authz.WriteGuard` and its closed `WriteOp` set live in the root package
  because the host implements it and both management surfaces take it up. A
  module asks it after shape checks and anti-escalation and before the mandatory
  audit and the `Manager`; a denial is a 403 with the `protected` code — the
  domain or target is protected, not the caller short a permission — and an
  unreadable answer a 503, and the seam can only narrow what the `Manager` would
  do. Delivering none is a legal assembly: the write routes then run without it.
  `WriteSettings` is a separate seam in the root package for the settings write:
  it takes only the tenant, its denial is the settings surface's `protected`
  code, and `WriteOp` keeps its closed three values. The host implements it and
  refuses a write to a personal tenant; an answer that cannot be read is a 503
  and never a permit. `settings.tenantadmin` requires it, so an assembly that
  mounts the tenant settings surface without one fails the startup; `WriteGuard`
  stays optional.
  The policy
  module's own writes — the seeding and reconciliation surfaces and the host's
  `Manager.ClearAccountBindings` account-deletion path — do not consult either
  seam.
- **The translation is this capability's rows unioned with every module's
  declared rows, and a permission no row covers is refused.** `internal/translation`
  carries the rows for this capability's own permissions, and a module that
  declares a permission of its own declares its (object, action) row beside it
  through `authz.TranslationSpec`; `authz.policy` and `authz.guard` each resolve
  the two into the one mapping the engine matches on, in their `Init`, before any
  route is checked. Resolving is a pure function of the registry and an
  idempotent write, so the two agree by construction; both do it because either
  may be the only one present, and a command that writes policy data without
  guarding a route takes the policy module and no HTTP entry point. A host reads
  the pair for a permission through `authz.Catalog.ObjectAction`. An action no
  resolved row covers is a denial without an error, so that an action the
  authentication side added is refused until a row is added here;
  `action_parity_test.go` holds the built-in rows to the two vocabularies. A
  module may not disagree with a row already in the set — a disagreement is a
  startup failure. `authn.tenant.access` is the one action with a platform-domain
  fallback, and it is the only one.
- **The engine or its store being unavailable is a 503, never a 403.** A
  decision that cannot be made is neither a denial nor a permission: the engine
  error is carried out so the caller can tell the two apart. A request-path
  refusal that is a denial, the endpoint a guarded route is registered on and
  the 403/503 write-out land with the enforcement middleware; the decider alone
  is what the enforcement module builds today.
- **The catalog declares the permission constants.** `policy` declares a
  `PermissionSpec` listing every permission, and the catalog is built by unioning
  the specs the registry holds — so a module declaring a permission of its own
  adds it the same way, and declares its translation row beside it, because a
  permission the translation cannot express is one no route may name.
  `TestTheCatalogDeclaresEveryPermission` in `action_parity_test.go` holds the
  permissions the modules it registers declare — `policy`'s own spec and a
  stand-in business module — to the root package's constants and to a translation
  row: a constant with no declaration, a declaration naming no constant, and a
  declared permission with no row each fail it. It sees only the modules it
  registers, so a host module that declares a permission with no row is not held
  here; the gate's `verify` holds it at startup instead, failing the process once
  the Init window closes when a declared route names a permission the catalog or
  the translation cannot express. The same guard holds a module's
  `TenantGoverned` to its own `Permissions`, and holds the platform codes —
  `authz.role.*` and `settings.*` — and the tenant codes —
  `authz.tenant.role.*` and `settings.tenant.*` — to their own translation
  pairs.
- **The sentinel set in the root package is closed over what this directory
  judges for itself.** A new class of failure is a decision, not an addition.
  Errors that pass through from an external library — Casbin's matcher errors,
  GORM's store errors — are not sentinels here and are carried out as they stand;
  every wrap on the way out uses `%w` so the chain stays whole.
- **`authztest` is for tests.** Its exported surface builds a stand-in enforcer,
  catalog and gate, and wires a registry and a configuration reader the way a
  host does; it imports the root package and never the policy or enforcement
  subpackage. The compiler cannot stop production code importing it; the
  dependency test in this directory fails `go test` for any package of the `pkg`
  module that imports it in a file that is not a test.
- **A secret, a credential and a policy value appear in no log line and no error
  text.** An account key and a tenant may be logged; a grant's rules and a
  binding's role reach no log. A refusal names the row that failed and never the
  value that failed it.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

This directory is a package tree of the `pkg` module, so it is built and tested
through that module:

```
go -C pkg build ./authz/...
go -C pkg test ./authz/...
go -C pkg test -race ./authz/...
env -C pkg golangci-lint run ./authz/...
```

The cases bring up an in-memory database or a handle the database module
delivered, and ask for no service beyond it. `policy/assembly_test.go` drives the
policy and guard descriptors through a real registry, which is where what each
callback takes up and what the two modules resolve from each other is observed;
`policy/policy_test.go` drives the management surface over a handle with the
tenant filter installed and holds the catalog's union of the registered specs;
`policy/migrate_test.go` runs the shipped migration files. `guard/decide_test.go` drives the decider over a stub engine. The snapshot
holder and the model self-check are tested under `pkg/authz/internal/policy`.
