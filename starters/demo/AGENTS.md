# starters/demo

A runnable, copyable skeleton of a speed product. Copy this directory, rename
it, and grow a business on it. It assembles this repository's modules into one
program — an HTTP entry point and its endpoints, a database engine, a key-value
and an event backend, the logging and telemetry providers, the operations
surface and the frontend host — and writes the command tree that says what each
run may be.

It is a starting point, not an evaluation host. The modules under `pkg/` deliver
the seams; this program shows one way to put them together. It ships no business
domain: `modules/` is the empty slot a product's own modules land in.

## Layout

| Path | What it is |
|---|---|
| `main.go` | The command tree, the deployment-stack table, the `gen` command, the placeholder command group, and `run()` |
| `host.go` | The environment prefix, the host's own module (the `config.HostIdentity` resource), and the endpoint constants |
| `modules/` | The slot for this product's business modules |
| `web/` | The frontend application; a placeholder today, built into `web/dist` and embedded by the Go program |
| `testdata/` | One configuration per deployment stack |
| `compose.yaml` | The four external backends the non-memory stacks run on |

## Running it

The binary is one program on five stacks. The stack is a level of the command
tree, and everything below a stack runs on it, so every command is written
`demo <stack> <command>`.

A run has to be pointed at a configuration. The locator is a URI, and its scheme
selects the transport, so a local file is written `file:///path/to/config.yaml`
— the scheme is not optional. It is given on the command line with `--config`,
after the command or before it, or through the environment variable
`DEMO_CONFIG`.

Build once, then run the two commands that stand a served deployment up — the
`sqlite-memory` stack needs nothing installed, so it is the one to start with:

```
# from the repository root
go -C starters/demo build -o /tmp/demo .

/tmp/demo --config "file://$PWD/starters/demo/testdata/sqlite-memory.yaml" sqlite-memory migrate
/tmp/demo --config "file://$PWD/starters/demo/testdata/sqlite-memory.yaml" sqlite-memory serve
```

`migrate` runs the stack's database engine alone and applies every module's
declarations; `serve` runs the whole lifecycle and accepts traffic. The program
writes a `ready` record on standard output once every module has started and a
`stopped` record as it leaves, so a supervisor (or a test) that needs to know the
process is up waits for the record and not for a listening socket. Stop a run
with SIGINT or SIGTERM; it drains in-flight requests first.

`version` answers with the program's version and assembles nothing:

```
/tmp/demo version
```

`gen` writes the merged API document. Like every command under a stack it reads
that stack's configuration, so it is pointed at one too; it binds no port and
constructs no module, so the configuration it reads is never connected to:

```
/tmp/demo --config "file://$PWD/starters/demo/testdata/sqlite-memory.yaml" \
    sqlite-memory gen --title demo-api --version 1.2.3 -o /tmp/openapi.json
```

The `serve` command's configuration is the sample under `testdata/`; the other
four stacks differ from it only in the backend section, and `compose.yaml`
brings those backends up:

```
docker compose -f starters/demo/compose.yaml up -d
/tmp/demo --config "file://$PWD/starters/demo/testdata/postgres.yaml" postgres migrate
/tmp/demo --config "file://$PWD/starters/demo/testdata/postgres.yaml" postgres serve
```

## Endpoints and guards

The product declares five endpoints, and what this program says about them is
which class of traffic belongs where:

| Endpoint | Traffic |
|---|---|
| `api` | the product's own API, scoped to a tenant |
| `account` | a user who has not chosen a tenant yet |
| `admin` | the platform's operators |
| `ops` | probes and documents; `GET /health`, `GET /ready`, `GET /openapi.json` |
| `web` | the frontend bundle; its catch-all route covers the endpoint's whole path space and it carries no other route |

Which guards each endpoint carries is configuration, and this skeleton ships no
guard module yet: every endpoint is assembled unguarded. A product adds the
modules that carry its guards — authentication, tenant resolution, the journal,
the rate limiter — to the commands it wants them on, exactly as the backend
modules are named today. `pkg/http` is where the endpoints and their chains are
declared and configured; the operations surface is `pkg/ops`, which mounts its
three routes on the endpoint `ops.endpoint` names and expects no guard there.

## The five stacks

A stack names a database engine, a key-value backend, an event backend and a
telemetry export backend. They are built by one function (`addStack`), so what
tells two of them apart is the table in `main.go` and nothing else:

| Stack | Engine | Key-value | Event | Export |
|---|---|---|---|---|
| `sqlite-memory` | `db.sqlite` | `kv.memory` | `event.memory` | `observability.stdout` |
| `postgres` | `db.postgres` | `kv.postgres` | `event.postgres` | `observability.stdout` |
| `postgres-redis-nats` | `db.postgres` | `kv.redis` | `event.nats` | `observability.otlp` |
| `postgres-nats-redis` | `db.postgres` | `kv.nats` | `event.redis` | `observability.stdout` |
| `postgres-memcached-nats` | `db.postgres` | `kv.memcached` | `event.nats` | `observability.stdout` |

The set is closed: a stack a product adds is a row in the `stacks` table, and a
configuration under `testdata/` to go with it.

The engine is named on the stack node, so every command under the stack runs it.
The key-value, event and export backends are named on the individual commands
instead, because `migrate` runs the engine alone: a backend named on the stack
node would run in `migrate` too, putting an input item and a reachability the
pre-deployment job has no use for.

## The commands

Under every stack:

- **`serve`** runs the whole lifecycle and accepts traffic.
- **`worker`** runs the background modules — the two backends and the telemetry
  providers — with no HTTP entry point.
- **`migrate`** runs the engine alone and reaches every module's declarations to
  build every table.
- **`gen`** merges the OpenAPI fragments the modules declare into one document
  and writes it out, either to the file `-o` names or to standard output; the
  document's title and version come from `--title` and `--version`, defaulting to
  the program's name and version. It drives to `StagePrepare`, so it binds no
  port and constructs no module: it reads the same static declarations the
  operations endpoint serves, through the same pure function (`pkg/ops/openapi`).
- The **command groups** are where a product's one-shot operations hang. The
  skeleton ships one placeholder group (`example`) with two leaves, one that
  stops at `Init` (a read) and one that goes to `Start` (a write); a product
  replaces the leaves' actions and words with its own.

## Where to change what

- **A new business module**: write it under `modules/` the way every module in
  this repository is written — a descriptor registered from an `init` — and name
  it on the commands in `main.go` that run it. The import is the wiring.
- **A new endpoint, guard or backend on an endpoint**: the endpoint is a
  constant in `host.go` and an entry under `http.endpoints` in the configuration;
  which modules guard it is the `Include` list of the command in `main.go`.
- **A new stack**: a row in the `stacks` table and a configuration under
  `testdata/`.
- **The frontend**: `web/`. The Go side embeds `web/dist` and serves it through
  `pkg/webui`; it changes only if the bundle's shape changes.

## Boundaries

- **The common pieces are taken from `pkg/`, not copied back in.** The
  operations surface is `pkg/ops` and its merge is `pkg/ops/openapi`; the
  frontend host is `pkg/webui`; a module that needs a logger names it through
  `pkg/log.NamedFor` rather than reaching for the process default. This program
  writes none of those and changes none of their contracts.
- **No business domain.** The skeleton carries no users, tenants, orders or any
  other domain type. A product's domain lives in `modules/` and on the command
  groups' leaves.
- **The command tree is this program's alone.** No module declares a command or
  learns which one runs it; adding one is an edit to `main.go`.
- **`main.go` writes the assembly, not the module contracts.** It calls
  `config.App`, `core.Module` and `core.Registry` and changes nothing about
  them; a module's own `AGENTS.md` is its contract.

## Running the checks

```
go -C starters/demo build ./...
go -C starters/demo test ./...
go -C starters/demo test -race ./...
```

The tests build the program once and run it as a real child process: they run
`migrate` and `serve` on the `sqlite-memory` stack and read the probes, and they
run `gen` and read the document it writes. The `sqlite-memory` stack needs
nothing installed; the other four need the backends `compose.yaml` brings up and
are not exercised by the suite.
