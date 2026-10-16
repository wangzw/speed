# starters/demo

A runnable, copyable skeleton of a speed product. Copy this directory, rename
it, and grow a business on it. It assembles this repository's modules into one
program — an HTTP entry point and its endpoints, the platform baseline below,
the logging and telemetry providers, the operations surface and the frontend
host — and writes the command tree that says what each run may be.

It is a starting point, not an evaluation host. The modules under `pkg/` deliver
the seams; this program shows one way to put them together. It ships no business
domain: `server/modules/` is the empty slot a product's own modules land in.

It is two trees under one directory. `server/` is the Go program — its own
module, its configuration samples and the frontend bundle it embeds; `web/` is
the frontend package, whose source and build tooling live there and whose build
output lands in `server/dist/`. The build output sits inside the Go tree because
`go:embed` reaches only files under the embedding package's own directory and a
pattern may not climb out with `..`.

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

The test of a doubtful module is the module's own contract: if its required
configuration items cannot be given a value that starts, it does not belong
here.

**The baseline carries WebAuthn (`authn.mfa.webauthn`), and it is the one
capability the criterion admits that a copy still has to be given
deliberately.** Its relying party is bound to the origin the deployment is
reached at — an item `testdata/` carries, not a credential or a service — so it
starts with nothing external and passes the test above. It is named here so the
skeleton can show what the platform's management surface is for: a step-up asks
for a factor kind the session has not already proved, and with TOTP as the only
kind a session that signed in with TOTP has nothing left to raise itself with, so
the step-up start is refused. TOTP beside WebAuthn gives the step-up a factor
kind to offer, and WebAuthn is the one the bundled deployment carries.

The bundled frontend carries the passkey step to completion: `@speed/auth-ui`,
`@speed/account-ui` and `@speed/console-ui` each drive the browser's WebAuthn
ceremony (`navigator.credentials.create`/`get`) from the step's own options and
report the credential it returns. Enrolling the passkey and completing the
step-up both work on the bundled UI (see "Re-authentication and impersonation on
a fresh copy" below).

**The baseline leaves out the static operator password table (`authn.static`),
though the criterion would admit it: it needs no external service, only the
table the configuration names.** It is the one backend that takes the Basic
scheme, and a browser opens its native password prompt for a Basic or Digest
challenge on any 401. This skeleton serves the frontend bundle to a browser, so
a 401 carrying Basic — a lapsed session or token on a guarded endpoint — would
put a native password box in front of a signed-in user instead of letting the
frontend end the session and return to its own login page. The session cookie
and the bearer token carry every caller the guards here reach, so the demo wires
no Basic backend and `testdata/` carries no password table; a copy that wants a
Basic-authenticated operator or probe adds `authn.static` and its
`authn.static.subjects` table back itself.

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
`server/main.go` carries these as blank imports beside a comment saying why; the
declarations of a module reached this way are collected by the stack node's
`IncludeAllDeclarations`, and no `Include` names it.

### The token signing key

`authn.token.signing-key-file` names `var/token-signing-key.pem`, a path relative
to the working directory. The program generates an ES256 key there on its first
run and keeps it afterwards: a key regenerated on every start would invalidate
every token already issued. The path is under `server/var/`, which `.gitignore`
keeps out of the tree, so a generated key is never committed. A deployment replaces the file
with its own key material — the token package's `GenerateKey("ES256")` mints one
to start from, and the token module itself neither generates nor rotates a key.

### Replace before serving real traffic

The configuration under `server/testdata/` ships development values so a copy
starts as it stands. Every one of them is marked `DEV` in the file it appears
in; replace them all before a deployment serves real traffic:

- **The initial operators** (`seed.accounts`) are the accounts the `seed`
  command writes. Replace every one of them before a deployment serves real
  traffic. The sample configurations give each a plaintext password, which is
  the one development value here written in the clear; a deployment that will
  not carry that gives a `password-hash` instead — a PHC entry, the form
  `pkg/authn/static`'s `Hash` helper writes. A seeded account signs in through
  the login flow (`authn.login.password`) and the session cookie it hands out,
  not through an Authorization scheme.
- **The database encryption key** (`db.<engine>.encryption-key`) seals the
  columns a module encrypts at rest. Replace the `dev-only-change-me-…` value.

## Layout

| Path | What it is |
|---|---|
| `Makefile` | The product's own build entry point: `make help` lists it, `make check` runs it. What a copy uses; inside the repository the root Makefile is the entry point |
| `server/main.go` | The command tree, the deployment-stack table, the baseline inclusion lists, the `gen` command, the placeholder command group, and `run()` |
| `server/host.go` | The environment prefix, the host's own module (the `config.HostIdentity` resource), and the endpoint constants |
| `server/signingkey.go` | The token signing key's path, and the first-run generation that puts a key there |
| `server/modules/` | The slot for this product's business modules |
| `server/testdata/` | One configuration per deployment stack |
| `server/dist/` | The frontend build output, embedded by the Go program |
| `web/` | The frontend application's source and build; it writes into `server/dist` |
| `compose.yaml` | The four external backends the non-memory stacks run on |
| `.gitignore` | Keeps the runtime state (`server/var/`) and the build output (`server/dist/`) out of the tree |

## Running it

The binary is one program on six stacks. The stack is a level of the command
tree, and everything below a stack runs on it, so every command is written
`demo <stack> <command>`.

A run has to be pointed at a configuration. The locator is a URI, and its scheme
selects the transport, so a local file is written `file:///path/to/config.yaml`
— the scheme is not optional. It is given on the command line with `--config`,
after the command or before it, or through the environment variable
`DEMO_CONFIG`.

Some of the configuration's values are paths relative to the working directory —
the token signing key, the object store's root, the sqlite stack's database — so
run the program from its module directory, `starters/demo/server`. The `sqlite`
stack needs nothing installed and keeps what it is given, so it is the one to
start with: seed the operator it ships with, then serve.

```
# from the repository root; -C runs the program with its module directory as the working directory
go -C starters/demo/server run . --config file:testdata/sqlite.yaml sqlite seed
go -C starters/demo/server run . --config file:testdata/sqlite.yaml sqlite serve
```

Or build once and run the binary from inside `starters/demo/server`, so the same
relative paths resolve:

```
go -C starters/demo/server build -o ./demo .
# then, with starters/demo/server as the working directory:
./demo --config file:testdata/sqlite.yaml sqlite seed
./demo --config file:testdata/sqlite.yaml sqlite serve
```

`migrate` runs the stack's database engine alone and applies every module's
declarations; `serve` runs the whole lifecycle and accepts traffic. The program
writes a `ready` record on standard output once every module has started and a
`stopped` record as it leaves, so a supervisor (or a test) that needs to know the
process is up waits for the record and not for a listening socket. Stop a run
with SIGINT or SIGTERM; it drains in-flight requests first.

`seed` writes the initial operators `seed.accounts` names, the tenants
`seed.tenants` names, and the platform-administrator role the ones marked with
it hold. It carries the migrations with it, so it is the whole of what a fresh
copy does to a database before serving from it; running it again writes nothing
and says so.

Seeding is a step of its own rather than something `serve` does on its way up,
because a deployment nobody may administer until somebody says so is the posture
this skeleton ships. On `sqlite-memory` the step achieves nothing — that stack's
database is gone with the process that opened it, and seed and serve are two
processes — which is why the stack to sign in on is `sqlite`.

`version` answers with the program's version and assembles nothing:

```
go -C starters/demo/server run . version
```

`gen` writes the merged API document. Like every command under a stack it reads
that stack's configuration, so it is pointed at one too; it binds no port and
constructs no module, so the configuration it reads is never connected to:

```
go -C starters/demo/server run . --config file:testdata/sqlite-memory.yaml \
    sqlite-memory gen --title demo-api --version 1.2.3 -o /tmp/openapi.json
```

The `serve` command's configuration is the sample under `server/testdata/`. The
four `postgres` stacks differ from the two `sqlite` ones in the backend section
alone, and `compose.yaml` brings those backends up:

```
docker compose -f starters/demo/compose.yaml up -d
go -C starters/demo/server run . --config file:testdata/postgres.yaml postgres migrate
go -C starters/demo/server run . --config file:testdata/postgres.yaml postgres serve
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

The five endpoints hang on two listeners, which the configuration under
`server/testdata/` declares: `api`, `account`, `admin` and `web` share the
`backend` listener, and `ops` has its own. A listener is one socket, so the
frontend bundle and the API are same-origin and a browser reaches the API from
the page without CORS. The `http.cors` policy the sample carries is therefore
inert in this stack; it is kept as the starting point for a deployment that
serves the frontend from an origin of its own.

Sharing a listener with the `web` catch-all decides what a path under no
endpoint's static prefix is answered with. On the `backend` listener the `web`
endpoint is the fallback, so such a path is served the frontend bundle. A path
that falls under an endpoint's prefix is not left to the bundle: that endpoint's
chain answers it even when no route registered the path. A mistyped path under
the api endpoint's prefix — `/api/v1`, which its routes establish — therefore
reaches the api endpoint, not the bundle: with no credentials the gateway
refuses it 401, and once admitted the api endpoint answers its own 404 (or its
405 where the path exists but the method is not allowed).

The baseline now ships the guards each endpoint carries, and the configuration
under `server/testdata/` names them:

- The gateway (`authn.gate`) guards `api`, `account` and `admin`. Its own
  routes — login, session, registration, profile, the account-management routes,
  invitations, impersonation and the enterprise provider register — mount on the
  `account` endpoint under `authn.gate.mount.base-path` (`/auth` by default).
  The login flow is delivered as a session cookie: the frontend's login flow
  requests `delivery: 'session'` (and `@speed/auth-core` forces it), so a
  completed login answers `{state: 'authenticated', session: {id, expires_at}}`
  with a `Set-Cookie` and no token pair, and a page reload stays signed in by
  re-reading `GET /auth/session`. The delivery set is a runtime setting
  (`authn.login.delivery-modes`, default `["session","token"]`), not a
  configuration item, so it is narrowed through the settings surface rather
  than `testdata/`; this host's frontend already requests session for every
  login.
- Tenant resolution (`tenant.resolve`) and admission (`tenant.admission`) guard
  `api`; the authorization guard (`authz.guard`) enforces `api`; the rate limiter
  (`ratelimit`) limits `api`. Resolution reads the tenant from the request's
  `X-Tenant` header (`tenant.resolve.header-name` in the stack file), so an `api`
  call without that header is refused 404 -- the tenant is not a claim of the
  access token, it is a request-time routing fact the caller must announce. The
  frontend does exactly that: the client built in `bootstrapSpeedApp` announces
  the host's current tenant as `X-Tenant` (its `tenantProvider`, wired from
  `starters/demo/web`), read per attempt.
- The platform's management surfaces mount on `admin`: the roster
  (`tenant.admin`), the authorization catalog (`authz.admin`), the settings
  surface (`settings.admin`) and the journal's query routes (`audit.admin`).
- The notification surface (`notification.inbox`, `notification.stream`,
  `notification.preferences`) mounts on `api`. The capability declares no
  permission point of its own, so its routes carry no gate and the authorization
  guard, whose default is closed, would refuse each one 403. The host closes that
  gap itself: `notification.access` (`server/notification.go`, named in
  `baselineServe`) registers the notification routes open on the guard's
  declaration surface, so the gateway still authenticates, the guard admits, and
  each handler answers for the caller. They are open rather than on a permission
  because they are scoped by the caller's identity — the inbox and the stream
  read the caller's own account — and not by anyone's permission. The preference
  routes read a recipient the host names on the request context
  (`preferences.WithRecipient`); this skeleton declares no notification type, so
  the matrix is empty and that step does not arise, and a product that declares
  one supplies the recipient.
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

### Re-authentication and impersonation on a fresh copy

The baseline names two factor kinds, TOTP and WebAuthn, and the second one is
here for the step-up. A step-up (`purpose: 'step-up'`) asks for a factor whose
authentication methods are disjoint from the session's, so an operator who
signed in with password and TOTP carries a session that proved `otp` and cannot
raise itself with another TOTP. WebAuthn's method (`webauthn`) is disjoint from
it, so the step-up start answers a `factor` step offering the passkey, and its
completion elevates the session (`Sessions.Elevate`), re-running the step-up-gated
write the operator tried -- an account-status or factor write, or an impersonation
start.

Both halves of the WebAuthn ceremony are in the bundled frontend. `@speed/auth-ui`,
`@speed/account-ui` and `@speed/console-ui` each drive the browser's WebAuthn
ceremony (`navigator.credentials.create` for a registration, `navigator.credentials.get`
for an assertion) from the step's own public-key options and report the credential
it returns: the enrollment wizard registers a passkey, and the step-up dialog
presents an assertion, both against whatever platform or roaming authenticator the
browser has. The second factor kind -- WebAuthn beside TOTP -- is what makes the
step-up start answer a factor step rather than refuse for want of one.

Two things order a hand-run of the sequence. The passkey has to be enrolled
*before* the session that raises itself: a step-up accepts only a factor enabled
before the session's authentication time, so a passkey enrolled under a session
is not offered to that same session. And the write has to be attempted with a
session that is no longer fresh: the gate is a fresh local multi-factor, its
window is the setting `authn.mfa.recent-window` (ten minutes by default), and a
just-completed multi-factor sign-in satisfies it directly, so the dialog appears
only once the window has passed.

Impersonation carries one gate of its own: its actor must hold an active
non-SMS factor activated at least `authn.mfa.sensitive-min-age` (24 hours by
default) before now, so an operator whose only factor was enrolled minutes ago
cannot start an impersonation until that factor matures. A freshly seeded copy
cannot demonstrate a started impersonation.

## The stacks

A stack names a database engine, a key-value backend, an event backend and a
telemetry export backend. They are built by one function (`addStack`), so what
tells two of them apart is the table in `main.go` and nothing else:

| Stack | Engine | Key-value | Event | Export |
|---|---|---|---|---|
| `sqlite` | `db.sqlite` | `kv.memory` | `event.memory` | `observability.stdout` |
| `sqlite-memory` | `db.sqlite` | `kv.memory` | `event.memory` | `observability.stdout` |
| `postgres` | `db.postgres` | `kv.postgres` | `event.postgres` | `observability.stdout` |
| `postgres-redis-nats` | `db.postgres` | `kv.redis` | `event.nats` | `observability.otlp` |
| `postgres-nats-redis` | `db.postgres` | `kv.nats` | `event.redis` | `observability.stdout` |
| `postgres-memcached-nats` | `db.postgres` | `kv.memcached` | `event.nats` | `observability.stdout` |

`sqlite` and `sqlite-memory` name the same modules and are still two stacks: the
database the first's configuration points at is a file under `var/`, and the
second's is the process's own, gone with the run that opened it. The table does
not show that difference and the configuration does — it is the one pair of rows
here whose distinction lives in `testdata/` alone. Sign in on `sqlite`; use
`sqlite-memory` to exercise the assembly and the probes, where nothing written is
meant to outlive the process.

The set is closed: a stack a product adds is a row in the `stacks` table, and a
configuration under `server/testdata/` to go with it.

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
- **`seed`** writes the accounts under `seed.accounts`, each with the credential
  it is given — a plaintext `password` or a `password-hash` — the tenants under
  `seed.tenants`, and the platform-administrator role the ones marked with it
  hold, carrying every permission the process declares. The role and its grants
  are written into two kinds of domain: the platform domain, which the
  platform's own management surface reads, and each tenant under `seed.tenants`,
  so the console's tenant-addressed pages — members, invitations and SSO
  providers — answer out of the box; a platform-domain binding does not reach a
  tenant domain. The reserved platform domain (`system`) is not accepted under
  `seed.tenants`: it names the platform's policy domain and not a tenant, and the
  policy module refuses to start when the roster holds a tenant carrying it, so
  the seed refuses the name before writing it rather than seeding a roster the
  next serve would not start on. It is idempotent — an account, a tenant, a role,
  a grant or a binding already there is left as it stands and reported as skipped
  — so it is safe to run at every deployment. Nothing else is touched, and an
  account or a tenant the configuration no longer names is neither disabled nor
  deleted. It drives to `StageStart`, past the migrations, so a database that was
  never migrated is one it can seed in a single command. A role and a binding come
  from here because no migration writes one: who a deployment's administrators
  are is the host's answer, and this command is where this host gives it.
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

- **A new business module**: write it under `server/modules/` the way every
  module in this repository is written — a descriptor registered from an `init` —
  and name it on the commands in `server/main.go` that run it. The import is the
  wiring.
- **The platform baseline**: `baselineServe` and `baselineWorker` in
  `server/main.go` are the two lists a product grows from. A module dropped from
  them is a capability the deployment does not carry; a module added is one it
  does, once its required configuration items are written into
  `server/testdata/`.
- **A new endpoint, guard or backend on an endpoint**: the endpoint is a
  constant in `server/host.go` and an entry under `http.endpoints` in the
  configuration; which modules guard it is the `Include` list of the command in
  `server/main.go`.
- **A new stack**: a row in the `stacks` table and a configuration under
  `server/testdata/`.
- **The frontend**: `web/`. Its build writes the bundle into `server/dist`, which
  the Go side embeds and serves through `pkg/webui`; the Go side changes only if
  the bundle's shape changes.

## Boundaries

- **The common pieces are taken from `pkg/`, not copied back in.** The
  operations surface is `pkg/ops` and its merge is `pkg/ops/openapi`; the
  frontend host is `pkg/webui`; a module that needs a logger names it through
  `pkg/log.NamedFor` rather than reaching for the process default. This program
  writes none of those and changes none of their contracts.
- **No business domain.** The skeleton carries no users, tenants, orders or any
  other domain type. A product's domain lives in `server/modules/` and on the
  command groups' leaves.
- **The command tree is this program's alone.** No module declares a command or
  learns which one runs it; adding one is an edit to `server/main.go`.
- **`server/main.go` writes the assembly, not the module contracts.** It calls
  `config.App`, `core.Module` and `core.Registry` and changes nothing about
  them; a module's own `AGENTS.md` is its contract.

## Running the checks

This directory carries its own `Makefile` — `make help` lists what it runs, and
`make check` runs all of it, the frontend first and the Go program second
because the Go build embeds the bundle. That file is the product's entry point:
a tree lifted out of the repository has nothing above it, so its build, test and
lint commands live there.

Two of its legs take an input a copy supplies and this repository does not:
`web-install` installs the frontend's dependencies where the frontend package
sits (inside the repository that package is a member of the workspace under
`web/`, whose single lockfile the repository's install goes through instead),
and `lint` reads the nearest `.golangci.yml` upward from the module directory
(this repository keeps one at its root).

**Inside this repository the entry point is the root Makefile.** `make check`
from the repository root covers this module through `go.work` like every other
module, and the frontend legs run as part of `make web-check`. A single leg
against this module alone is `make -C starters/demo <target>`.

The repository's own build leg adds a `GOWORK=off` build beside the workspace
one. That is the standalone build: a copy dropped outside this repository
resolves `pkg` from its released version, so its `go.mod` and `go.sum` have to
carry every dependency the imports reach.

The tests build the program once and run it as a real child process: they run
`migrate` and `serve` on the `sqlite-memory` stack, read the probes and the
merged document, and read two of the identity system's own routes to hold that
the baseline is assembled and not only the infrastructure it began with. The
`sqlite-memory` stack needs nothing installed; the other four need the backends
`compose.yaml` brings up and are not exercised by the suite.
