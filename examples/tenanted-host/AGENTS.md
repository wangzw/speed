# examples/tenanted-host

A host that puts statements behind a tenant: the database engine, an HTTP entry
point, the authentication gateway with one credential backend, the tenant
release unit — resolution and roster — and one application module whose model
embeds the scoping convention.

It is a sibling of `examples/authenticated-host` rather than an extension of
it. That host exists to show an entry point behind authentication and assembles
no database at all; this one exists to show what happens to a query once a
tenant is settled, which needs an engine, a table and a second tenant to
compare against.

## What it is for

Three things no module's own suite can hold:

- **The query really comes back scoped.** `listOrders` writes a `Find` with no
  condition in it, and the rows that arrive are one tenant's. Whether the
  filter reached that statement is not observable from inside `pkg/tenant`,
  because it happens in a GORM callback installed on a handle that a different
  module built.
- **A refusal happens before a handler runs.** Authentication refuses a request
  with no credential, and that request never drives a roster lookup — the
  layer's place on the chain is a fact about the assembled chain, not about
  either module.
- **A command reaches a module for its declarations without running it.**
  `migrate` runs the engine and nothing else, and the application module's
  table is built anyway. The application module is not constructed there, its
  input items are not on that command's line, and a case reads the resulting
  schema out of the database file rather than taking the command's word for it.

## Boundaries

- **The command tree is the host's, and it is in `main.go`.** No module
  declares a command and none can learn which command took it in.
- **`pkg/tenant/catalog` is imported blank, and its name is nowhere in the
  command tree.** The roster runs because the resolving module declares a
  construction-time dependency on the capability it delivers. Importing the
  package is what puts it in the available set; writing its name down as well
  would say the same thing twice, so this host says it once.
- **The route that needs no tenant sits on the endpoint that resolves none.**
  Resolution is configured per endpoint and has no per-route exception, so
  there is no way to exempt a route from it. The two endpoints here are how a
  host says which traffic is scoped; a configuration that guards one endpoint
  with both guards and leaves the other to neither is the whole of it.
- **The background path states what it is doing.** `seed` runs outside any
  request, where there is no resolving layer to establish a tenant and the
  writing side of the context is closed to everything but the release unit. It
  crosses tenants and writes the tenant column itself, which is the only shape
  a non-request path has. The roster needs none of that: its table does not
  embed the convention, so its statements are neither conditioned nor refused.
- **A scoped table leads its primary key with the tenant column.** The
  migration does, and the comment there says why. This is not only a query
  plan: a unique constraint that does not lead with the tenant column lets the
  insert a `Save` or an `OnConflict` produces conflict on another tenant's row
  and update that row into the writer's tenant. Nothing in that statement names
  another tenant, so there is no refusal to be had — the key is what stops it.
  The model and the migration have to declare the same key, and
  `pkg/tenant/catalog/plugin_test.go` holds the upsert case.

## Running it

```
go -C examples/tenanted-host build ./...
go -C examples/tenanted-host test ./...
```

The cases compile the host once in `TestMain` and run it as a child process,
against a SQLite file in a temporary directory and on ports taken from the
operating system. They need the Go toolchain and a loopback interface, and
nothing else installed.

By hand:

```
go -C examples/tenanted-host run . migrate --config file:///path/to/config.yaml
go -C examples/tenanted-host run . serve   --config file:///path/to/config.yaml
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
    dsn: "/var/lib/tenanted-host/host.db"
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
```

Each module hangs under its own release unit's heading — `authn.gate` beside
`authn.static`, `tenant.resolve` beside `tenant.catalog`, `db.sqlite` alone —
and none of them owns the heading itself.

Two things a run of this binary will not do on its own. It does not register
any tenant: a host does that through the roster's write surface, and `seed`
here is the smallest form of it. And it does not resolve a tenant anywhere but
on the endpoints written under `tenant.resolve.endpoints` — a request to
another endpoint carries no tenant, and any scoped statement issued there is
refused rather than left unfiltered.
