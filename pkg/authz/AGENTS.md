# pkg/authz

The authorization capability directory: deciding whether an identified subject
may do a thing on a resource. The interface package a consumer of a permission
imports; `pkg/authz/policy` — the module that owns the policy tables and their
migrations, the thin store the engine reads and writes through, the permission
catalog, the engine snapshot the decisions read, the manager that reads and
writes the policy data, and the account lifecycle cleanup — `pkg/authz/guard`,
the module that implements the authentication capability's decision seam, and
`pkg/authz/authztest`, the fixtures a test of this directory drives. The thin
adapter the engine stores its policy through, the snapshot holder, and the one
permission translation live under `pkg/authz/internal/`, which only this
directory can import.

The management surface's routes are this directory's third module,
`authz.admin`: it mounts them under `/authz/admin/*`, delivers nothing of its
own, and is not built here yet.

The API is the package's godoc. Read the capability interfaces' comments
before writing a consumer.

## Modules and namespaces

- **The root package `pkg/authz` registers no module and exports no
  `ModuleName`.** It is imported by every consumer of a permission — the
  enforcement point, the management surface and any host code that names a
  permission — so a module registered here would put the policy engine into the
  available set of every binary that touches any of them and occupy a module
  name.
- **`pkg/authz/policy` registers `authz.policy`; `pkg/authz/guard` registers
  `authz.guard`.** Each registering package exports its own `ModuleName`, and
  that same value is its configuration and settings namespace; every segment of
  the name is a lower-case letter, a digit or a dash. Each of the two carries
  its own `AGENTS.md` beside it, stating that package's boundaries.
- **`pkg/authz/internal/persist`, `pkg/authz/internal/policy` and
  `pkg/authz/authztest` register no module.** The two `internal` packages are
  the directory's library units and `authztest` is a test fixture.
- **Namespaces do not overlap, and the root package owns none.** Two modules
  under this directory may not declare one input-item path, and no module's path
  may be a leaf of another's interior; `authz` itself is no module's leaf name.
- **What the namespaces hold today.** `authz.policy` declares the configuration
  item `authz.policy.load-timeout` and the setting
  `authz.policy.refresh-interval`. The enforcement module's namespace is
  `authz.guard`; it declares no input item yet, and the endpoint list and the
  undeclared-route default arrive with its middleware.

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
- **The seeding write is a capability of its own.** `Manager` refuses every
  write on a builtin role; `Seeder` adds grants to one and never removes,
  rewrites or deletes a grant. `authz.policy` delivers both exclusively, and
  `authz.admin` takes up `Manager` alone — so the wall between the management
  surface and a builtin role's grant set is the capability the module holds,
  not a branch in a handler. A management route would reach `SeedGrants` only
  if the module were given a `Seeder`, and its descriptor names none.
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
  the translation cannot express.
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
