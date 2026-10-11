# examples/orders-host

A host that assembles a working set of this repository's modules into one
running program: an HTTP entry point, the database engine, the authentication
gateway with one credential backend, the tenant capability directory —
resolution and roster — the audit journal, the rate limiting layer, the runtime
settings surface, a key-value and an event backend, and the telemetry providers,
together with two application modules of its own. It writes the command tree
that says what each run may be, and lets that tree drive the lifecycle.

It is a working set and not a census: what is here is what the cases below need
to hold, and a module this host does not assemble is a module whose composition
nothing here shows.

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
- **The limiting layer lands where the configuration and its own declaration
  put it.** The rate limiting module is named on `serve` and registers a layer
  on the endpoint the configuration names, and on that endpoint it sits inside
  authentication, tenant resolution and the journal — the three anchors its own
  declaration carries, so the chain listing can be read as positions that
  landed rather than as spellings. Neither its reach nor its position is
  observable from inside `pkg/ratelimit`: the layer registers itself through the
  entry point and never sees the chain it landed on, and an anchor that no layer
  stands for is dropped with nothing said about where the layer ended up.
- **A published fact reaches a subscriber that does not know the publisher.**
  The placement module publishes under a type name; the inventory module counts
  what it is handed without reading the payload or importing the publisher.
- **A refusal happens before a handler runs, and before resolution.**
  Authentication sits outside resolution on the endpoint's chain, so a request
  without a credential is refused without a roster lookup ever happening. The
  entry point runs the same layers on a request that matches no route and on one
  whose method the matched path does not allow, so the 404 and the 405 reach only
  a caller the guards let through, and a caller without a credential learns from
  neither which paths exist. Every refusal a layer or the entry point writes is a
  problem details document (`application/problem+json`); the host's own handlers
  answer their failures with a bare status.
- **The gateway guards every endpoint unless the configuration narrows it.**
  Left out, `authn.gate.endpoints` means `*`, so the `ops` endpoint is refused
  without a credential; an empty list, or `*` written beside a named endpoint,
  stops the startup naming the key. This host narrows the list to `public`, so
  the health check on `ops` needs none, and the chain listing and the startup
  diagnostics are where the difference is readable.
- **A password entry is read in each form it can be written in, and the caller
  is `static.<subject>`.** An Argon2id entry from the `hash` command, a PBKDF2
  entry in the PHC form and an entry in the older form all let their subject in
  through Basic; the older one is named in a startup warning that carries no part
  of any entry. The identity the handler reads is the account `static.alice`, and
  the records written after authentication carry it as `account`, beside
  `backend`.
- **A command reaches a module for its declarations without running it.**
  `migrate` runs the engine and nothing else, and the application module's
  table, the journal's and the settings surface's are built anyway.
- **A route nobody instrumented produces a span.** The telemetry layer is
  registered on an endpoint by configuration, so every route on that endpoint is
  covered without a handler asking for anything, and the destination the signals
  leave through is a module the command tree names rather than something the
  assembler knows about. Neither half is visible from inside `pkg/http` or
  `pkg/observability`.

## Boundaries

- **The command tree is the host's, and it is in `main.go`.** No module
  declares a command and none can learn which command took it in.
- **`serve` names every module it means to run.** `IncludeAll()` would make the
  participating set a function of the import graph, and on a path carrying
  tenant scoping that means a module nobody wrote down could carry a statement
  filter into the run.
- **Every exclusive provider is named, the two backends and the roster
  included.** Naming one is the only way it can be in the run: the closure
  expands a required capability into its declared providers except the ones
  that claim it exclusively, because picking one of several implementations is
  the host's decision and the expansion rule makes no decisions. A capability
  nobody claims exclusively is expanded instead, which is what lets several
  implementations of one capability be assembled side by side.
- **`migrate` takes the whole available set rather than a list.**
  `IncludeAllDeclarations()` has a silent failure of its own — a module left out
  of it has its tables quietly not built — so a list is the wrong shape for it.
- **The route that needs no tenant sits on the endpoint that resolves none.**
  Resolution is configured per endpoint and has no per-route exception, so there
  is no way to exempt a route from it. The two endpoints here are how a host
  says which traffic is scoped.
- **The background path states what it is doing.** `seed` runs outside any
  request, where there is no resolving layer to establish a tenant. A path that
  works for one tenant enters it through the roster's `Enter`; `seed` writes
  the rows of several tenants in one pass, work that belongs to none of them,
  so it crosses tenants and writes the tenant column itself, and it records that
  crossing through the audit seam with the purpose stated — the seam exists for
  exactly the decisions no capture path can see. The roster needs none of that:
  its table does not embed the convention, so its statements are neither
  conditioned nor refused. `seed` puts its tenants rather than creating them,
  because it writes the same roster on every run; a registration that must not
  overwrite a tenant that is there is a `Create`.
- **A scoped table leads its primary key with the tenant column.** The
  migration does, and the comment there says why. This is not only a query plan:
  a unique constraint that does not lead with the tenant column lets the insert
  a `Save` or an `OnConflict` produces conflict on another tenant's row and
  update that row into the writer's tenant. Nothing in that statement names
  another tenant, so there is no refusal to be had — the key is what stops it.
- **A route is spelled one way everywhere it is named.** The handlers are gin
  handlers registered with `Route(method, path, ...)`, the path in gin's spelling
  (`/orders`, `/orders/:id`). The gateway's exemption and the journal's `requests`
  entries (`METHOD /path`, one string each) name a route by that method and
  that path, and a request is compared with them character for character. A
  spelling that differs is well formed and never matches, and nothing reports it:
  an exemption that never matches leaves the route guarded, and a request entry
  that never matches leaves the journal without the row. Each case that expects
  one of those rows has to see it. The journal's object for a request row is the
  route path, with the method in the action column.
- **Only a test imports `authntest`.** The package builds the context a gateway
  would have left on a request, so non-test code that imported it could make an
  identity no gateway stamped. `authntest_test.go` parses every non-test source
  file of this module for its imports, whatever its build constraints say, and
  holds the host to it, since the check in `pkg/authn` does not reach a module of
  its own.
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
    # Left out, every endpoint is guarded; this narrows it.
    endpoints:
      - public
  static:
    subjects:
      alice: "$argon2id$v=19$m=19456,t=2,p=1$...$..."
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
ratelimit:
  endpoints:
    - public
observability:
  otel:
    service-name: orders-host
    endpoints:
      - public
```

Each module hangs under its own capability directory's heading — `authn.gate`
beside `authn.static`, `tenant.resolve` beside `tenant.catalog`,
`audit.journal` under `audit` — and none of them owns the heading itself.

The gateway guards every endpoint the entry point declares unless
`authn.gate.endpoints` narrows it, and an empty list is a startup failure rather
than a way to switch it off. This host narrows it to `public`, because the `ops`
endpoint serves the health check with no credential.
The identity a handler reads for the configured account is `static.alice`: the
static prefix, then the subject's name.

Two things a run of this binary will not do on its own. It does not register any
tenant: a host does that through the roster's write surface, and `seed` here is
the smallest form of it. And it does not resolve a tenant anywhere but on the
endpoints written under `tenant.resolve.endpoints` — a request to another
endpoint carries no tenant, and any scoped statement issued there is refused
rather than left unfiltered.
