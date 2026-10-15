# starters/demo

A runnable, copyable skeleton of a speed product. Copy this directory, rename
it, and grow a business on it. It assembles this repository's modules into one
program — an HTTP entry point and its endpoints, the platform baseline below,
the logging and telemetry providers, the operations surface and the frontend
host — and writes the command tree that says what each run may be.

It is a starting point, not an evaluation host. The modules under `pkg/` deliver
the seams; this program shows one way to put them together. It ships no business
domain: `modules/` is the empty slot a product's own modules land in.

## The platform baseline

The program names the platform baseline: every platform capability that starts
without a credential, an external account or a service outside the deployment
stack. What that leaves out is the capability that needs one of those, and a
product adds it once it holds the dependency:

| Left out | Why |
|---|---|
| `authn.login.google`, `authn.login.github`, `authn.login.wechat-web`, `authn.login.wechat-mp` | a social provider's client credentials |
| `authn.sso.oidc`, `authn.sso.saml` | an enterprise provider's client credentials and metadata |
| `sms.*`, `authn.login.sms`, `authn.mfa.sms`, `authn.sms` | an SMS vendor's account |
| `mail.smtp` | an SMTP account |
| `objectstore.s3` | an S3 account and bucket |
| `authn.mfa.webauthn` | a relying party bound to a real origin |

The test of a doubtful module is the module's own contract: if its required
configuration items cannot be given a value that starts, it does not belong
here.

The baseline is written twice in `main.go`, as `baselineServe` and
`baselineWorker`. `serve` stands the HTTP entry point up and names every module
with a route or a middleware; `worker` names the background modules that need
neither a router nor a gateway — the roster, settings, the job queue and its
scheduler, and the outbox relay. The stack's engine, key-value backend, event
backend and export provider are named on the commands rather than on the stack
node, so `migrate` still runs the engine and the declarations alone.

### What a run names and what the imports reach

Naming a module in the command tree and importing its package answer two
different questions, and the baseline depends on both:

- **The command tree decides what a process runs.** A module an `Include` list
  names takes its place in that run; a module nothing names is never
  assembled and never started.
- **The binary's imports decide what the contract document covers.** `gen` and
  `/openapi.json` merge the `http.Spec` fragments of every module the imports
  reach — the available set — whether or not a command names that module for a
  run.

So every platform module that declares an HTTP fragment is imported, including
the ones the credential criterion keeps out of every run (`sms.admin`,
`authn.sso.saml`): their fragments still describe the platform's API surface.
Leaving one unimported would drop its operations from the merged document with
no error — the document would simply be smaller than the platform contract.
`main.go` carries these as blank imports beside a comment saying why; the
declarations of a module reached this way are collected by the stack node's
`IncludeAllDeclarations`, and no `Include` names it.

### The token signing key

`authn.token.signing-key-file` names `var/token-signing-key.pem`, a path relative
to the working directory. The program generates an ES256 key there on its first
run and keeps it afterwards: a key regenerated on every start would invalidate
every token already issued. The path is under `var/`, which `.gitignore` keeps out
of the tree, so a generated key is never committed. A deployment replaces the file
with its own key material — the token package's `GenerateKey("ES256")` mints one
to start from, and the token module itself neither generates nor rotates a key.

### Replace before serving real traffic

The configuration under `testdata/` ships development values so a copy starts as
it stands. Every one of them is marked `DEV` in the file it appears in; replace
them all before a deployment serves real traffic:

- **The operators' password table** (`authn.static.subjects`) holds an Argon2id
  hash of the password `operator-dev-secret` for the subject `operator` (the
  account `static.operator`). Write your own entry with the static package's
  `Hash` and replace it.
- **The database encryption key** (`db.<engine>.encryption-key`) seals the
  columns a module encrypts at rest. Replace the `dev-only-change-me-…` value.

## Layout

| Path | What it is |
|---|---|
| `main.go` | The command tree, the deployment-stack table, the baseline inclusion lists, the `gen` command, the placeholder command group, and `run()` |
| `host.go` | The environment prefix, the host's own module (the `config.HostIdentity` resource), and the endpoint constants |
| `signingkey.go` | The token signing key's path, and the first-run generation that puts a key there |
| `modules/` | The slot for this product's business modules |
| `web/` | The frontend application, built into `web/dist` and embedded by the Go program |
| `testdata/` | One configuration per deployment stack |
| `compose.yaml` | The four external backends the non-memory stacks run on |
| `.gitignore` | Keeps `var/` — the object store's local root and the generated token signing key — out of the tree |

## Running it

The binary is one program on five stacks. The stack is a level of the command
tree, and everything below a stack runs on it, so every command is written
`demo <stack> <command>`.

A run has to be pointed at a configuration. The locator is a URI, and its scheme
selects the transport, so a local file is written `file:///path/to/config.yaml`
— the scheme is not optional. It is given on the command line with `--config`,
after the command or before it, or through the environment variable
`DEMO_CONFIG`.

Two of the configuration's values are paths relative to the working directory —
the token signing key and the object store's root — so run the program from
`starters/demo`. The `sqlite-memory` stack needs nothing installed, so it is the
one to start with:

```
# from the repository root; -C runs the program with starters/demo as its working directory
go -C starters/demo run . --config file:testdata/sqlite-memory.yaml sqlite-memory migrate
go -C starters/demo run . --config file:testdata/sqlite-memory.yaml sqlite-memory serve
```

Or build once and run the binary from inside `starters/demo`, so the same
relative paths resolve:

```
go -C starters/demo build -o ./demo .
# then, with starters/demo as the working directory:
./demo --config file:testdata/sqlite-memory.yaml sqlite-memory migrate
./demo --config file:testdata/sqlite-memory.yaml sqlite-memory serve
```

`migrate` runs the stack's database engine alone and applies every module's
declarations; `serve` runs the whole lifecycle and accepts traffic. The program
writes a `ready` record on standard output once every module has started and a
`stopped` record as it leaves, so a supervisor (or a test) that needs to know the
process is up waits for the record and not for a listening socket. Stop a run
with SIGINT or SIGTERM; it drains in-flight requests first.

`version` answers with the program's version and assembles nothing:

```
go -C starters/demo run . version
```

`gen` writes the merged API document. Like every command under a stack it reads
that stack's configuration, so it is pointed at one too; it binds no port and
constructs no module, so the configuration it reads is never connected to:

```
go -C starters/demo run . --config file:testdata/sqlite-memory.yaml \
    sqlite-memory gen --title demo-api --version 1.2.3 -o /tmp/openapi.json
```

The `serve` command's configuration is the sample under `testdata/`; the other
four stacks differ from it only in the backend section, and `compose.yaml`
brings those backends up:

```
docker compose -f starters/demo/compose.yaml up -d
go -C starters/demo run . --config file:testdata/postgres.yaml postgres migrate
go -C starters/demo run . --config file:testdata/postgres.yaml postgres serve
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

The baseline now ships the guards each endpoint carries, and the configuration
under `testdata/` names them:

- The gateway (`authn.gate`) guards `api`, `account` and `admin`. Its own
  routes — login, session, registration, profile, the account-management routes,
  invitations, impersonation and the enterprise provider register — mount on the
  `account` endpoint under `authn.gate.mount.base-path` (`/auth` by default).
- Tenant resolution (`tenant.resolve`) and admission (`tenant.admission`) guard
  `api`; the authorization guard (`authz.guard`) enforces `api`; the rate limiter
  (`ratelimit`) limits `api`.
- The platform's management surfaces mount on `admin`: the roster
  (`tenant.admin`), the authorization catalog (`authz.admin`), the settings
  surface (`settings.admin`) and the journal's query routes (`audit.admin`).
- The journal (`audit.journal`) records the requests its `requests` list names
  on the endpoints its `endpoints` list names.
- `ops` and `web` carry no gateway: the operations surface serves probes and the
  document, and the frontend bundle is static.

Which guards each endpoint carries is configuration, and where a module mounts
its routes — the two endpoint lists on the tenant layers, the guard's list, the
journal's requests — is a decision to revisit as a product's own routes grow.
`pkg/http` is where the endpoints and their chains are declared and configured;
the operations surface is `pkg/ops`, which mounts its three routes on the
endpoint `ops.endpoint` names and expects no guard there.


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

- **`serve`** runs the whole lifecycle and accepts traffic: the entry point, the
  platform baseline (`baselineServe` in `main.go`), the stack's two backends and
  the telemetry providers.
- **`worker`** runs the background capabilities — the roster, settings, the job
  queue and its scheduler, and the outbox relay, beside the two backends and the
  telemetry providers — with no HTTP entry point (`baselineWorker` in `main.go`).
  A module whose routes or request lists are read wherever it runs belongs to
  `serve`, which stands the entry point up.
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
- **The platform baseline**: `baselineServe` and `baselineWorker` in `main.go`
  are the two lists a product grows from. A module dropped from them is a
  capability the deployment does not carry; a module added is one it does, once
  its required configuration items are written into `testdata/`.
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
GOWORK=off go -C starters/demo build ./...
go -C starters/demo test ./...
go -C starters/demo test -race ./...
env -C starters/demo golangci-lint run ./...
```

The `GOWORK=off` build is the standalone one: a copy dropped outside this
repository resolves `pkg` from its released version, so its `go.mod` and `go.sum`
have to carry every dependency the imports reach. Run the linter from inside the
module directory: a trailing absolute path is matched against the module root of
the working directory and can report zero issues without loading the package.

The tests build the program once and run it as a real child process: they run
`migrate` and `serve` on the `sqlite-memory` stack, read the probes and the
merged document, and read two of the identity system's own routes to hold that
the baseline is assembled and not only the infrastructure it began with. The
`sqlite-memory` stack needs nothing installed; the other four need the backends
`compose.yaml` brings up and are not exercised by the suite.
