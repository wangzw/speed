# pkg/authz

The authorization capability directory: deciding whether an identified subject
may do a thing on a resource. The interface package a consumer of a permission
imports; `pkg/authz/policy` — the module that owns the policy tables and their
migrations, the thin store the engine reads and writes through, the permission
catalog, the engine snapshot the decisions read, the manager that reads and
writes the policy data, and the account lifecycle cleanup — `pkg/authz/guard`,
the module that implements the authentication capability's decision seam and
holds the one permission translation, and `pkg/authz/authztest`, the fixtures a
test of this directory drives. The thin adapter the engine stores its policy
through and the snapshot holder live under `pkg/authz/internal/`, which only
this directory can import.

The management surface's routes are this directory's third module,
`authz.admin`: it mounts them under `/authz/admin/*`, delivers nothing of its
own, and is not built here yet.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authz.md`](../../docs/design/modules/design-authz.md).
- The decisions behind it, one file per decision, in
  [`docs/adr/`](../../docs/adr/): `adr-authz-model-2026-10-12`,
  `adr-authz-engine-2026-10-12`, `adr-authz-store-2026-10-12`,
  `adr-authz-decision-seam-2026-10-12`, `adr-authz-enforcement-2026-10-12`,
  `adr-authz-cache-2026-10-12`, `adr-authz-packaging-2026-10-12` and
  `adr-authz-reversal-2026-10-12`.
- The API itself: the godoc in this package. Read the capability interfaces'
  comments before writing a consumer.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

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
  undeclared-route default the design places under it arrive with its
  middleware.

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
  matcher does not fail to load — it silently allows or denies where the design
  says the opposite — so `policy` runs the self-check fixtures in
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
- **The decision is a closed translation, and an unknown action is refused.**
  `guard` holds the one mapping from a permission to the (object, action) pair
  the engine matches on. An action with no row is a denial without an error, so
  that an action the authentication side added is refused until the table is
  updated; `action_parity_test.go` holds the table and the two vocabularies
  aligned. `authn.tenant.access` is the one action with a platform-domain
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
  adds it the same way. `TestTheCatalogDeclaresEveryPermission` in
  `action_parity_test.go` holds the declared permissions to the root package's
  constants: a constant with no declaration and a declaration naming no constant
  each fail it.
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
