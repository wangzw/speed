# examples/crm-host

A multi-tenant B2B CRM assembled from this repository's modules, built to
evaluate them: whether what `pkg/` delivers is enough to write a real product
on, and whether it behaves the way its design says. It runs one binary on five
deployment stacks and drives every stack through the same end-to-end cases.

This directory is the skeleton the CRM's feature slices are added to: users,
sessions and memberships, the platform operators' admin surface, the
operations endpoint and the command line. A slice adds files of its own and
edits no shared list:

- a module file that registers itself with `registerApplication`, naming the
  commands it runs on, the models it issues statements on and the routes worth
  a journal row;
- its migrations under `migrations/<module>/{sqlite,postgres}/` and its OpenAPI
  fragment under `openapi/`;
- if it has command-line actions, a command group registered with
  `registerCommandGroup`;
- its claims under its own heading in the package comment in `main.go`, and its
  cases in a test file of its own.

The command tree takes each module into the commands it names, the harness
configures the journal from the routes it lists, and the migrate, interrupt,
OpenAPI and wiring cases read the registration table, so a new module is wired
and checked without an edit anywhere else.

## What it is for

A defect the host runs into is a defect of the module, and it is fixed there.
The host is written the way each module's contract says it should work, so a
case that fails is evidence about a module rather than about the host — which
is only true while the host never bends around what it finds.

The package comment in `main.go` lists what a run demonstrates, by area. Each
claim there has its case in `e2e_test.go` or a slice's own test file, or in
`wiring_test.go` where the property leaves no trace in a run.

## Layout

| File | What it holds |
|---|---|
| `main.go` | The package comment, the stack table and the command tree, with the platform modules each command runs. |
| `host.go` | The host identity (prefix `CRM`) and the endpoint names. |
| `application.go` | The registration table every host module goes through, the release records, the error body, the route and request-body helpers, identifiers. |
| `users.go` | The global users table, password hashing at the cost `users.password-iterations` gives — a hash at another cost is derived again at it on its user's next sign-in — and the credential backend for Basic credentials of CRM users (an open claim). |
| `sessions.go` | The bearer backend (the `bearer` scheme claimed whole), sessions in the key-value store, the `sessions.ttl` setting, and `POST /login`, `POST /logout` on the account endpoint. |
| `members.go` | The scoped memberships table, the membership layer on the API endpoint, `GET`/`POST /members`, and `GET /me` on the account endpoint. |
| `admin.go` | The operator check and the roster, user and cross-tenant grant routes on the admin endpoint. |
| `ops.go` | Health, readiness, the merged OpenAPI document, and the ready record. |
| `cli.go` | The shared command-line flags, the command-group mechanism, and the tenant and user command actions. |
| `migrations/<module>/{sqlite,postgres}/` | Each table-owning module's own migrations. |
| `openapi/` | Each module's OpenAPI fragment, embedded by the module that declares it. |

## Boundaries

- **The host never hand-writes a tenant predicate or a tenant column on a
  statement the filter scopes.** A scoped statement issued from a request uses
  the request's context and names no tenant: a read carries no tenant
  condition, and a row saved from a request leaves `TenantID` empty for the
  filter to fill. `member.TenantID` is for answering with, never for writing
  into a scoped row. The only statements that name a tenant are the
  cross-tenant ones — the admin endpoint's grant, the command line, `GET /me` —
  and each says so with `tenant.CrossTenant`, takes the tenant from a roster
  read rather than a constant, and states why in a comment; a write among them
  states its purpose through the audit seam as well. A path with no request
  behind it that works for one tenant — a subscriber, a command, an operator's
  read of one tenant's trail — enters that tenant through the roster instead
  of crossing tenants: `Enter` for work that waits on the tenant being served,
  `EnterRegistered` for work that still belongs to a disabled tenant.
- **The host never retries, wraps or routes around a module call to paper over
  a defect.** No retry loop, no second code path, no fallback, no second
  statement shape chosen because the first misbehaves. A case that goes red
  because a module misbehaves stays red — never skipped, never weakened —
  until the module is fixed.
- **A route is one string, `METHOD /path`, with the path in gin's grammar.**
  `:name` is a parameter and `*name` a catch-all, and the path is in the form
  gin keeps it in: a leading slash, no repeated slash. `mount` splits the
  string for `Endpoint.Route`, a module's `journalled` list hands it to the
  journal's `requests` item as it is, and the gateway's exemption takes the
  same method and path. The journal and the gateway compare the request's
  method and the matched route's path with that pair character for character,
  so a spelling no route carries is a well-formed entry that never matches and
  reports nothing: a case that expects a route's row reads that row. The
  journal's row has the method as its action and the path as registered as its
  object — `/contacts/:id`, not the path the client sent, which is in its
  detail.
- **A request body is bound with `bindBody`, and the host adds no strictness to
  what the binder reads.** It is gin's `ShouldBindJSON` followed by the body
  type's own `Validate`; a body past the endpoint's limit answers 413, one the
  type cannot hold 400, and one `Validate` refuses 422. A member the type does
  not have is passed over, member names match without regard to case, the last
  of a repeated member stands, and a JSON null leaves the target as it was;
  where a case depends on any of these, it says so and asserts the binder's
  reading rather than a stricter one the host would have to bring back.
- **The command tree is the host's.** `main.go` names the platform modules
  each command runs; each host module names the commands it runs on in its
  registration, and the tree takes it in by name. `IncludeAll` is not used,
  because on a command carrying tenant scoping it would let the import graph
  decide which statement filters run. Every exclusive provider is named, since
  the dependency closure never picks one.
- **The stack is a level of the command tree, and the stacks differ in the
  stack table alone.** One function builds every stack's subtree. The stack
  node takes in the engine at run level and every declaration at static level,
  and every command under it inherits both: `migrate` needs the declarations to
  build every table, and each other command drives past Migrate, so it has to
  carry the same static inclusion as `migrate` (design-db.md). The key-value,
  event and export backends are named on the commands that run a module
  needing them, not on the stack node, because inclusion is inherited and
  `migrate` runs the engine alone.
- **Every module that issues statements on a scoped model requires
  `tenant.Catalog` and lists the model.** The tenant filter is installed only in
  a command that runs the roster; the requirement turns a command that forgot
  it into a startup failure rather than a run that writes an empty tenant and
  reads every tenant's rows. `wiring_test.go` holds every registered module to
  the requirement for the models it lists, and reads the source for every type
  embedding `tenant.Model` so none goes unlisted.
- **A many-to-many association between scoped models writes both tenant
  halves into one column.** The field's `joinForeignKey` and `joinReferences`
  both name `TenantID` first, so the tenant half of the owner's key and of the
  target's lands in one column of the join table; the tenant filter takes
  that column for the join table's tenant column, whoever builds the join
  model, and scopes every statement on the table by it. The join model is the
  one GORM builds from the tag: the host writes none and names none with
  `SetupJoinTable`. `wiring_test.go` holds every `many2many` field between
  scoped models of the listed models to that tag, and a declaration from which
  no one tenant column can be taken — GORM's default of a column per side
  among them — is refused by the tenant module on its first statement, which
  the stakeholder case would show on every stack.
- **Every module that holds a logger requires `log.Logger`.** Each application
  module with a product holds a Named logger for its release records.
- **An application module stops after the requests in flight are answered.**
  The registry stops every module that declares `Serve` first — `http` is
  this host's one, and its Stop returns once its drain is over — then every
  other module, then closes them all, each in reverse construction order. So
  an application module's Stop and Close follow the drain wherever it was
  constructed, and a Stop may stop taking work without refusing a request
  still being answered; the drain case holds every module's stop record to
  that. Work a bus hands a subscriber is outside it: the bus and its
  subscribers stop in reverse construction order. Handles a handler reads
  belong to the modules that deliver them.
- **A table referencing another module's table requires that module's table
  token.** Migrations are applied in the order the requirements give, with the
  module name breaking ties, and nothing else orders two modules' migrations.
  `users` delivers `usersTable` and `members` delivers `membersTable` for this
  alone — tokens nothing resolves, claimed exclusively so a requirement on them
  never draws their module into a command. Make the requirement `Optional`
  unless the module also needs the other at run time: the migration order
  counts optional requirements as well, and an optional one does not force the
  other module into every command that runs yours. `wiring_test.go` reads
  every migration's `REFERENCES` and fails a reference no requirement orders.
  The SQLite locator carries `_pragma=foreign_keys(1)`, so a reference is
  enforced there as on PostgreSQL.
- **A transaction is written in the order its logic reads, on every engine.**
  The database module begins every SQLite transaction IMMEDIATE, holding the
  database's write lock from its BEGIN and waiting for it there within the
  driver's busy timeout, so a check followed by a write waits out a
  concurrent writer as it does on PostgreSQL. The locator names no
  transaction mode: `_txlock=deferred` there would give back SQLite's own
  default, whose transaction takes the lock at its first write and is refused
  it outright when it has read first while another connection writes. The
  host never reorders a transaction's statements to suit an engine.
- **The CRM principal is `member.UserID`.** A CRM user authenticates through
  two backends — `users` for Basic, `sessions` for a bearer token — so the
  identity's `Key()` is `users/<id>` or `sessions/<id>` depending on the
  credential. Store the user identifier (`member.UserID`, or `userIDOf` off the
  API endpoint) wherever a row records who acted. The platform modules key on
  `Key()`, so the rate limiter keeps one bucket per credential kind and the
  journal names the actor by the credential used: a consequence of the
  two-backend shape, not something the host corrects.
- **A layer that must see the journal or the rate limiter anchors on them.**
  The membership layer declares `After` on the gateway, resolution, the journal
  and the limiter. Anchoring on the gateway and resolution alone leaves it
  unordered against the journal and the limiter: its own zero `Order` is
  smaller than the journal's, so it lands outside the journal, and outside the
  limiter, which sits inside the journal. Its refusals would be neither
  recorded nor counted.
- **The journal covers the account and admin endpoints, which resolve no
  tenant.** Its layer anchors after resolution on every endpoint it is
  registered on, so each serving start lists that anchor on those two
  endpoints as having landed on no layer, at Info: no layer there stands for
  resolution, which the HTTP module reports as the legal assembly it is,
  keeping warning level for a layer that anchors on itself. The rows it writes
  there carry no tenant, which is what those endpoints serve. The chain case
  fails on any other constraint that lands on no layer, so a layer a slice
  adds whose anchor names nothing on its endpoint is caught by name.
- **A scoped table leads its primary key with the tenant column and checks it
  is not empty.** The migration's comment says why.
- **Each module owns its tables, its migration directory and its OpenAPI
  fragment.** The operations endpoint serves one document merged from every
  fragment in the binary, and one document holds an operation once: a route's
  method and path are unique across all four endpoints, and a repeat fails the
  startup naming both modules and both endpoints.
- **Uniqueness is the constraint's to decide.** A look-up before an insert
  answers the ordinary duplicate sooner and nothing more; the unique index or
  the primary key is what refuses the concurrent one, and a reference is what
  refuses a row another table still names. `pkg/db` marks those refusals with
  `gorm.ErrDuplicatedKey` and `gorm.ErrForeignKeyViolated` on both engines, so
  a write judges them with `errors.Is` and answers 409; it never imports a
  driver to read the driver's own error. The constraint's name is only on the
  driver's error, so an answer that could come from two constraints names
  both.
- **The roster a replica reads is its snapshot.** `Get`, `ByAlias` and `List`
  answer from this replica's own writes and its last refresh; `SetStatus`
  and `Create` check the table itself, and `Enter` and `EnterRegistered` read
  a tenant's row from the table wherever the snapshot would refuse it, so a
  path with no request behind it enters a tenant another replica registered
  at once. A route that needs a tenant another replica registered — a
  request's resolution among them — sees it only after a refresh. A
  registration goes through `Create`, never `Get` followed by `Put`: `Create`
  refuses an identifier the table holds whatever the snapshot says, where
  `Put` would rewrite that tenant's alias and status. A deployment of several
  replicas — and a case that writes through one replica and reads through
  another — sets `tenant.catalog.refresh-period`.
- **Every key-value key the host writes carries a tenant identifier or a random
  component.** The store has no namespaces, and on Memcached the cases sharing
  one server keep apart by their keys alone.
- **Command-line inputs are flags.** The `cli` module's schema carries the
  shared ones (`--tenant-id`, `--tenant-alias`, `--email`, `--display-name`,
  `--role`); it runs on every command group, so a slice's actions read them
  with `readCLIInput`. A slice's own inputs go in a flag-only schema its module
  declares under a namespace of its own, with flag names nothing else in the
  binary uses: a flag name is one argument across the whole manifest. Nothing
  on the command line is positional. A command that writes drives to Start,
  where the journal accepts seam records; a command that reads drives to Init.
- **The command groups run no event backend.** A one-shot command on a
  transport bus pays for the bus on every run. A configuration that asks the
  journal to capture events stops every command-line write at startup, naming
  the journal's event list, and whoever writes that configuration decides:
  empty the list for one-shot commands, as the worker empties its endpoint
  lists, or name the bus on the groups with that cost written down.
- **A worker shares the serving configuration and empties two endpoint lists.**
  The journal and the telemetry assembler take the HTTP entry point up when
  their endpoint lists name endpoints, and the serving configuration writes
  names into both while the worker runs no entry point; the harness starts it
  with `CRM_AUDIT__JOURNAL__ENDPOINTS=` and
  `CRM_OBSERVABILITY__OTEL__ENDPOINTS=`. Emptying the telemetry list is needed
  because the serving configuration lists names: the assembler's default list,
  `*`, is skipped when no entry point is present.
- **Keep the demonstrated claims and their cases together.** A claim the
  package comment makes and no case covers is one the next reader cannot trust.

## Running it

```
go -C examples/crm-host build ./...
go -C examples/crm-host test ./...
go -C examples/crm-host test -race ./...
env -C examples/crm-host golangci-lint run ./...
```

The cases compile the host once in `TestMain` and run it as a child process;
under `-race` the host is built with the race detector as well, and a child that
reports a data race fails its case. Such a child runs with the detector's
second-long sleep before exit turned off, and every case derives passwords at a
low cost — `users.password-iterations` and the operator's hash, at
`caseHashIterations` — since at the production cost one derivation takes over a
second under the detector and the cases make hundreds. The `sqlite-memory`
stack needs nothing but the Go toolchain and a loopback interface. The four
PostgreSQL stacks need the docker CLI: the harness starts, or reuses when
already running, one container per backend per machine —
`speed-crmtest-postgres`, `speed-crmtest-redis`, `speed-crmtest-nats` and
`speed-crmtest-memcached` — on ports docker chooses.
Each container carries a label with a digest of the image and arguments the
harness creates it from; one created from other constants is refused with the
`docker rm -f` that removes it, and is never removed by the harness, since
another test binary may be using it. Each case gets a database of its own on
PostgreSQL, a database index of its own on Redis, and a namespace and bucket of
its own on NATS, and gives each back when it ends. A machine without docker
skips those stacks unless `CI` is set, in which case they fail. Remove the
containers by hand with `docker rm -f` when they are no longer wanted.

By hand, on the stack that needs nothing installed:

```
go -C examples/crm-host run . sqlite-memory migrate --config file:///path/to/crm.yaml
go -C examples/crm-host run . sqlite-memory tenant create --tenant-id t_acme --tenant-alias acme --config file:///path/to/crm.yaml
go -C examples/crm-host run . sqlite-memory user create --email alice@acme.test --display-name Alice --config file:///path/to/crm.yaml < password.txt
go -C examples/crm-host run . sqlite-memory user grant --tenant-id t_acme --email alice@acme.test --role owner --config file:///path/to/crm.yaml
go -C examples/crm-host run . sqlite-memory serve --config file:///path/to/crm.yaml
```

A configuration this host serves on, with the operator's hash from
`crm-host hash`:

```yaml
http:
  endpoints:
    api:     {address: "127.0.0.1:8080"}
    account: {address: "127.0.0.1:8081"}
    admin:   {address: "127.0.0.1:8082"}
    ops:     {address: "127.0.0.1:8083"}
db:
  sqlite:
    dsn: "/var/lib/crm-host/crm.db?_pragma=foreign_keys(1)"
authn:
  gate:
    endpoints: [api, account, admin]
  static:
    name: ops
    subjects:
      root: "pbkdf2-sha256$600000$...$..."
tenant:
  resolve:
    endpoints: [api]
    extractor: header
    header-name: X-Tenant
  catalog:
    refresh-period: 30s
audit:
  journal:
    endpoints: [api, account, admin]
    requests: ["GET /members", "POST /members", "POST /login", "POST /tenants"]
ratelimit:
  endpoints: [api]
observability:
  otel:
    service-name: crm-host
    endpoints: [api, account, admin]
```

The static backend's name has to be `ops`: the admin endpoint lets through the
identities that backend owns and no others.

## Extending the harness

`harness_test.go` runs the binary; `backends_test.go` brings up what the stacks
run on; `links_test.go` puts a proxy a case can cut between the host and each
backend; `telemetry_test.go` is the OTLP receiver, which decodes what the OTLP
backend exports; `fixture_test.go` is a case's API; `client_test.go` makes
requests; `children_test.go` keeps a killed test binary's children from
outliving it.

- `forEachStack` runs a case on every stack as a parallel subtest;
  `forEachSharedStack` on the stacks several replicas can share.
- `newCase(t, stack)` lays out a stack's configuration. Add a slice's own
  items with `c.config.set("path.to.item", value)` before starting a run. The
  PostgreSQL pools are capped per process (`db.postgres.max-open-conns`,
  `kv.postgres.max-conns`, `event.postgres.max-conns`); a load case raises them
  the same way. `log.level` stays `info`: readiness and the release records are
  read from Info records, and a case that moves it fails at once. The endpoint
  table is a map, which only the configuration file gives and every launch
  writes whole with the addresses it reserved; an item of one endpoint goes in
  with `c.setOnEndpoint(endpoint, key, value)`.
- Inside a subtest, take a view of the case with `c := c.in(t)`: a view's
  helpers fail the subtest rather than its parent, and share the case's
  database, configuration and claims.
- `c.migrate()`, `c.createTenant(name)`, `c.disableTenant(ref)`,
  `c.createUser(name)`, `c.grant(tenant, user, role)` go through the command
  line; `h.adminCreateTenant(t, ref)`, `h.adminCreateUser(t, user)` and
  `h.adminGrant(t, tenant, user, role)` do the same through the admin endpoint
  as the operator. `c.tenantNamed(name)` and `c.userNamed(name)` make the
  names unique per case. `c.setGlobal(key, value)` writes a global setting row
  before a run starts.
- Passwords are derived at `caseHashIterations`. Both credential backends
  verify a hash at the count it carries, so a case does not depend on the
  count; one whose property rests on how long a derivation takes sets
  `users.password-iterations` to the production cost itself, as the case of
  two creations of one email at once does.
- One-shot commands and the worker run on a configuration whose endpoints are
  all at `127.0.0.1:0`; they bind nothing. `c.serve()` and
  `c.serveReplicas(n, together|firstThenRest)` reserve each replica's ports
  right before launching it, from a set no two cases in the binary share, and
  relaunch a replica once on fresh ports if another process took one first.
  `together` launches every replica before waiting, as a rollout does;
  `firstThenRest` waits for the first before launching the rest, for a case
  whose property is not the rollout. A rollout that fails reports every
  replica's outcome and output at once. Every configuration file is replaced
  whole, by a rename.
- `c.serve()`, `c.serveReplicas(...)` and `c.worker()` wait for the ready
  record and stop the run at cleanup, holding it to exit status 0 and its
  stopped record; a run that never became ready is only terminated. When a case
  fails, the output of every run it started is logged, even one that stopped
  cleanly.
- `h.get`, `h.post` and `h.send` take a tenant alias and a credential —
  `basic(user)`, `bearer(token)`, `operator()`, `noCredential{}` — and fail the
  test when the request cannot be made; `h.do` returns the error instead, for
  requests sent from several goroutines at once.
- `c.journal()` and `c.awaitJournal(...)` read `audit_entries`; `c.sql()` is
  the harness's own connection for anything else a case asserts on.
- `c.holdWrites(table)` holds every write to a table back while its reads go
  through, until the function it returns is called, and
  `c.awaitWritersHeld(table, n)` waits until n writes are held: on PostgreSQL
  until the server lists them waiting for the table's lock, on SQLite for a
  fixed pause, since nothing there lists a waiting writer. A case whose
  property is a constraint deciding between concurrent writes sends them while
  the writes are held, so every one has made its look-up before any commits.
- Each launch arms a watchdog that kills the child shortly before the test
  binary's deadline, children are killed by the kernel with the binary on
  Linux, and every binary records its running children so the next one to
  start ends those of a binary that is gone.
- `newLinkedCase(t, stack)` and `forEachLinkedStack(t, reaches, body)` lay out
  a case whose host processes reach the database, the key-value store and the
  bus each over a link of its own — a TCP proxy in the test process, one per
  link even where two links reach one container — on the stacks `reaches`
  picks (`reachesDatabase`, `reachesStore`, `reachesBus`). `c.cut(link)` closes
  every connection the link carries and each new one as soon as it is
  accepted, and `c.restore(link)` lets new ones through again;
  `c.requireCarried(link)` fails a case whose cut would reach nothing. The
  harness's own connections go to the servers directly and are never cut. A
  backend added to `backends` passes its locator through `c.through(link, ...)`.
- `p.kill(t)` ends a run with SIGKILL, as a crash would: nothing it holds is
  given back and nothing it took is acknowledged, and its stop is not held to
  a clean exit.
- On the stack that exports through the OTLP backend, `c.receiver` holds every
  span and metric that arrived, decoded, with the resource of the process that
  exported it: `spansNamed`, `metricsNamed` and `resources`, and
  `requireDecoded(t)` fails a case an undecodable export reached. A run's
  spans and metrics are all exported once the run has stopped.
- A new stack is a row in `stacks` in `main.go`; a backend no stack used before
  is an entry in `backends` in `backends_test.go`.
