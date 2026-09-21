# examples/orders-host

A host that assembles every module of this repository into one running program:
an HTTP entry point, the database engine, the authentication gateway with one
credential backend, the tenant release unit — resolution and roster — the audit
journal, the runtime settings surface, and a key-value and an event backend,
together with two application modules of its own. It writes the command tree
that says what each run may be, and lets that tree drive the lifecycle.

It assembles the whole set at once, and what it exists to show is that the set
composes: that a setting a module declares is read back through three layers,
that a write is captured by the journal four ways, that a fact published under a
type name reaches a subscriber that never named the publisher, and that all of
it still happens behind authentication and inside a tenant.

## What it is for

The things no module's own suite can hold:

- **The query really comes back scoped, and the read really falls back.**
  `listOrders` writes a `Find` with no condition in it and gets one tenant's
  rows; the limit it applies comes from the settings surface, so the same
  response shows which of the three layers — tenant row, global row, declared
  default — answered. Neither is observable from inside `pkg/tenant` or
  `pkg/settings`: the filter reaches a handle another module built, and the
  snapshot lives behind a surface this module only reads.
- **The journal's capture paths each write a row.** The statement, the request
  that carried it, the event it published and the seam call the seed makes are
  four separately configured paths, and a run that captured three of them would
  pass every case that names one. The seam is the one path whose caller knows
  *why* — a cross-tenant write outside any request — which is the fact no
  capture path can supply.
- **A published fact reaches a subscriber that does not know the publisher.**
  The placement module publishes under a type name; the inventory module counts
  what it is handed without reading the payload or importing the publisher.
- **A refusal happens before a handler runs, and before resolution.**
  Authentication sits outside resolution on the endpoint's chain, so a request
  without a credential is refused without a roster lookup ever happening.
- **A command reaches a module for its declarations without running it.**
  `migrate` runs the engine and nothing else, and the application module's
  table, the journal's and the settings surface's are built anyway.

## Boundaries

- **The command tree is the host's, and it is in `main.go`.** No module
  declares a command and none can learn which command took it in.
- **`serve` names every module it means to run.** `IncludeAll()` would make the
  participating set a function of the import graph, and on a path carrying
  tenant scoping that means a module nobody wrote down could carry a statement
  filter into the run. The one thing this host does not name is the pair of
  backends: they arrive through the requirements the modules here declare, which
  is the half of the wiring that stays right when a module is added.
- **`pkg/tenant/catalog` is imported blank, and its name is nowhere in the
  command tree.** The roster runs because the resolving module declares a
  construction-time dependency on the capability it delivers. Importing the
  package is what puts it in the available set; writing its name down as well
  would say the same thing twice.
- **`migrate` takes the whole available set rather than a list.**
  `IncludeAllDeclarations()` has a silent failure of its own — a module left out
  of it has its tables quietly not built — so a list is the wrong shape for it.
- **The route that needs no tenant sits on the endpoint that resolves none.**
  Resolution is configured per endpoint and has no per-route exception, so there
  is no way to exempt a route from it. The two endpoints here are how a host
  says which traffic is scoped.
- **The background path states what it is doing.** `seed` runs outside any
  request, where there is no resolving layer to establish a tenant and the
  writing side of the context is closed to everything but the release unit. It
  crosses tenants and writes the tenant column itself, and it records that
  crossing through the audit seam with the purpose stated — the seam exists for
  exactly the decisions no capture path can see. The roster needs none of that:
  its table does not embed the convention, so its statements are neither
  conditioned nor refused.
- **A scoped table leads its primary key with the tenant column.** The
  migration does, and the comment there says why. This is not only a query plan:
  a unique constraint that does not lead with the tenant column lets the insert
  a `Save` or an `OnConflict` produces conflict on another tenant's row and
  update that row into the writer's tenant. Nothing in that statement names
  another tenant, so there is no refusal to be had — the key is what stops it.
- **An example stays an example.** Nothing here is imported by a released
  module, and nothing here is a place to put code the modules should carry: if a
  host would have to write it again, it belongs in a module.
- **Keep the demonstrated cases and their tests together.** A case the package
  comment in `main.go` claims and `e2e_test.go` does not cover is a lie the next
  reader has no way to catch. The few whose property leaves no trace in a run
  are in `wiring_test.go`, which reads the descriptors instead.

## Running it

```
go -C examples/orders-host build ./...
go -C examples/orders-host test ./...
```

The cases compile the host once in `TestMain` and run it as a child process,
against a SQLite file in a temporary directory and on ports taken from the
operating system. They need the Go toolchain and a loopback interface, and
nothing else installed — no container, no external service.

By hand:

```
go -C examples/orders-host run . migrate --config file:///path/to/config.yaml
go -C examples/orders-host run . serve   --config file:///path/to/config.yaml
```

A configuration this host serves on:

```yaml
http:
  endpoints:
    public:
      address: "127.0.0.1:8080"
    ops:
      address: "127.0.0.1:8081"
db:
  sqlite:
    dsn: "/var/lib/orders-host/host.db"
authn:
  gate:
    endpoints:
      - public
  static:
    name: local
    subjects:
      alice: "pbkdf2-sha256$600000$...$..."
tenant:
  resolve:
    endpoints:
      - public
    extractor: header
    header-name: X-Tenant
  catalog:
    refresh-period: 30s
audit:
  journal:
    endpoints:
      - public
    requests:
      - "GET /orders"
    events:
      - order.placed
settings:
  refresh-interval: 30s
```

Each module hangs under its own release unit's heading — `authn.gate` beside
`authn.static`, `tenant.resolve` beside `tenant.catalog`, `audit.journal` under
`audit` — and none of them owns the heading itself.

Two things a run of this binary will not do on its own. It does not register any
tenant: a host does that through the roster's write surface, and `seed` here is
the smallest form of it. And it does not resolve a tenant anywhere but on the
endpoints written under `tenant.resolve.endpoints` — a request to another
endpoint carries no tenant, and any scoped statement issued there is refused
rather than left unfiltered.
