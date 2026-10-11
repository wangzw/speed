# examples/crm-host

A multi-tenant B2B CRM assembled from this repository's modules, built to
evaluate them: whether what `pkg/` delivers is enough to write a real product
on, and whether it behaves the way its design says. It runs one binary on five
deployment stacks and drives every stack through the same end-to-end cases.

This directory is the skeleton the CRM's feature slices are added to: the
accounts, their sessions and memberships (the identity system of `pkg/authn`
and `pkg/tenant`), the platform operators' admin surface, the operations
endpoint and the command line. A slice adds files of its own and edits no
shared list:

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
| `main.go` | The package comment, the stack table and the command tree, with the platform modules each command runs, the identity system's own modules among them. |
| `host.go` | The host identity (prefix `CRM`) and the endpoint names. |
| `application.go` | The registration table every host module goes through, the migration tree helper every module's migrations use, the release records, the error body, the route and request-body helpers, identifiers. |
| `decider.go` | The host's `authn.Decider`: it allows the platform operator a management action and refuses `authn.tenant.access` whatever the actor, so an operator stays a stranger to every tenant until a permission model is written. |
| `members.go` | The scoped `member_roles` table (the role an account holds in a tenant; the membership relation itself is the account module's), `GET`/`POST /members`, and `GET /me` on the account endpoint. The member check is `tenant.admission`, not a layer of this module. |
| `admin.go` | The operator check and the roster, user and cross-tenant grant routes on the admin endpoint. A user is created through `authn.AccountAdmin`, and the operator is judged by the full account `static.<operator subject>`. |
| `ops.go` | Health, readiness, the merged OpenAPI document, and the ready record. |
| `cli.go` | The shared command-line flags, the command-group mechanism, and the tenant and user command actions, the user actions going through the account store. |
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
  Each table-owning host module that another host module references delivers a
  table token for this alone — a token nothing resolves, claimed exclusively so
  a requirement on it never draws its module into a command. Make the
  requirement `Optional` unless the module also needs the other at run time:
  the migration order counts optional requirements as well, and an optional one
  does not force the other module into every command that runs yours.
  `wiring_test.go` reads every migration's `REFERENCES` and fails a reference
  no requirement orders, and it fails a host migration that references an
  identity-system (`authn_`) table: the account identifiers are checked through
  the interfaces, not by a cross-module foreign key. The SQLite locator carries
  `_pragma=foreign_keys(1)`, so a reference is enforced there as on PostgreSQL.
- **A transaction is written in the order its logic reads, on every engine.**
  The database module begins every SQLite transaction IMMEDIATE, holding the
  database's write lock from its BEGIN and waiting for it there within the
  driver's busy timeout, so a check followed by a write waits out a
  concurrent writer as it does on PostgreSQL. The locator names no
  transaction mode: `_txlock=deferred` there would give back SQLite's own
  default, whose transaction takes the lock at its first write and is refused
  it outright when it has read first while another connection writes. The
  host never reorders a transaction's statements to suit an engine.
- **The CRM principal is `member.Account`.** A CRM user authenticates with the
  session cookie the login flow set, through the session module's cookie
  backend, so the identity's account, and its `Key()`, is the account the flow
  resolved (`member.Account`). Store the account identifier wherever a row
  records who acted: `owner`, `owner_id` and `actor_id` carry it and name no
  foreign key. The platform modules key on `Key()`, so the rate limiter keeps
  one bucket per account and the journal names the actor by the account. An
  operator is the account `static.<operator subject>`: the admission layer
  answers it as a stranger to every tenant, with the same 404 it answers a
  non-member.
- **One scheme has one backend.** The operators' table takes `basic`, so a CRM
  user's Basic header reaches no backend of the library: the gateway hands
  Basic to the one backend that takes it, the operators' table, which does not
  know the user. A CRM user logs in through the login flow's password method
  and carries the session cookie afterwards. A case that needs a CRM user on
  the API signs the user in (`signedIn`, which runs the flow on first use and
  sends the cookie), and a case about a Basic credential on the API expects
  the operators' table to refuse it.
- **A layer that must see the journal or the rate limiter anchors on them.**
  `tenant.admission` declares `After` on the gateway, resolution, the journal
  and the limiter. Anchoring on the gateway and resolution alone would leave it
  unordered against the journal and the limiter, so its refusals would be
  neither recorded nor counted.
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
  their endpoint lists are not empty, and the worker runs none; the harness
  starts it with `CRM_AUDIT__JOURNAL__ENDPOINTS=` and
  `CRM_OBSERVABILITY__OTEL__ENDPOINTS=`.
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
second-long sleep before exit turned off, and every case makes a derivation cheap
where it can. `createUser` and the static table's operator entry are written with
`static.HashPBKDF2` at a low count and handed to the create path as
`PasswordHash`, so a creation pays no derivation of its own. The account module's
own derivation settings carry a floor, so lowering them cannot cheapen a
derivation; an imported entry is read and bounded and not judged, which is what
the module offers a test. The static backend names its below-floor operator entry
at Warn on startup, and `TestTheStartupNamesTheWeakOperatorEntry` holds that line
rather than the case suppressing it. By default the
backend derives at most as many hashes at once as the process has processors,
and answers 503 to a request that finds none free, so a case that sends two
operator requests together first sends one alone, to warm the cache: on one
processor, two requests that both find it cold are answered one 200 and one 503. The `sqlite-memory`
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

`user create` reads its credential from standard input; with `--password-hash`
that input is an entry already in the account module's PHC form, imported as it
is, which is how a caller creates an account without paying a derivation of its
own.

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
    mount:
      endpoint: account
      public-url: "https://crm.example.com"
  static:
    subjects:
      ops: "$argon2id$v=19$m=19456,t=2,p=1$...$..."
  token:
    issuer: "https://crm.example.com"
    audience: ["https://crm.example.com"]
    algorithm: ES256
    signing-key-file: "/var/lib/crm-host/signing-key.pem"
    verify-key-files: ["/var/lib/crm-host/previous-key.pem"]
tenant:
  resolve:
    endpoints: [api]
    extractor: header
    header-name: X-Tenant
  admission:
    endpoints: [api]
  catalog:
    refresh-period: 30s
audit:
  journal:
    endpoints: [api, account, admin]
    requests: ["GET /members", "POST /members", "POST /tenants"]
ratelimit:
  endpoints: [api]
observability:
  otel:
    service-name: crm-host
    endpoints: [api, account, admin]
```

The admin endpoint lets through the one account `operatorAccount` names,
`static.ops`: the subject of that name in the operators' table, which only the
static backend may hand back. Another subject of the table authenticates and is
answered 403 there. The identity system's own routes are mounted on the account
endpoint under `authn.gate.mount.base-path` (default `/auth`), so a login runs
through `POST /auth/flows` and a logout through `POST /auth/logout` there, and
the token surface runs there too (`POST /auth/token`, `POST /auth/token/revoke`
and `GET /auth/jwks.json`). The `authn.token` section names the signing key file
and the algorithm; `authn.token.issuer` defaults to
`authn.gate.mount.public-url`, the audience to the issuer, and the client
identifier to `speed`, so a deployment that names none still issues tokens a
verifier can compare.
`authn.gate.endpoints` narrows the gateway's default of every endpoint to the
three that carry credentials, which leaves the `ops` endpoint with the probes
and the documents unguarded. `tenant.admission.endpoints` holds the account and
admin endpoints out of the member check: the account endpoint resolves no
tenant, and the admin endpoint is the operator's.

## The identity system

The identity system of `pkg/authn` and `pkg/tenant` is what a CRM user, a
session and a membership are. `main.go` names its modules on `serve`: `mount`
carries the route mounter the login, session and multi-factor modules mount
through; `authn.account` is the account store and delivers the account reads and
writes, the password capability, the membership store and the failure back-off;
`authn.session` is the session store and the cookie backend; `authn.login` and
`authn.login.password` are the login flow and its password method; `authn.mfa`
is the factor register and policy; `authn.token` is the access and refresh
tokens and registers the bearer verifier; `tenant.admission` holds the member
check on the API endpoint; `authz` (this host's `decider.go`) is the decision
seam. Every one of them is named, since the dependency closure never picks an
exclusive provider.

What this means for a slice:

- **A CRM user's identifiers, credentials and memberships live in the account
  module's tables.** The host keeps no users table and derives no password: a
  user is created through `authn.AccountAdmin.Create`, its email written as an
  administrator's assertion, its password checked against the policy. The host's
  own tables name an account identifier and carry no foreign key into the
  identity system's tables.
- **A CRM user reaches the API with the session cookie the login flow set.** The
  login is `POST /auth/flows` (method `password`, input `identifier` and
  `password`) and, once it answers `next.kind: complete`, `POST /auth/flows/answer`
  with `step: complete`, which sets `__Host-sid`. A logout is `POST /auth/logout`.
- **A CRM user can take the same login as a token pair.** The same flow with
  `delivery: token` answers `access_token` and `refresh_token`; the access token
  reaches the guarded routes as `Authorization: Bearer`, and the token module's
  verifier stamps it as the account the flow resolved. `POST /auth/token` serves
  the `refresh_token` grant (and `client_credentials`, which this host issues no
  service accounts for), `POST /auth/token/revoke` is RFC 7009, and
  `GET /auth/jwks.json` publishes the verification keys. All three are Open; the
  first two are registered through the mounter's `Callback`, the key set through
  `Public`. The signing key comes from a file the configuration names, and the
  module refuses to start with no key: the token tests mint one with
  `token.GenerateKey` into the case's directory.
- **The membership check is `tenant.admission`.** A caller that is not a member
  of the tenant its request names is answered the same 404 as an unknown tenant;
  the host keeps only the role (`member_roles`), which admission does not know.
- **The role every handler gates on is read from `member_roles`, last.** A route
  reads the caller's role with `memberOf`/`memberWith`, which read the tenant's
  own table through the scoped handle.
- **`subtree`, which every module's migrations go through, lives in
  `application.go`, so a slice's migration tree stands on its own.**

## The one-shot import

`import run` moves a deployment's data out of the previous migration set's
tables and into the identity system. It is a one-shot command of the host's own
command-group mechanism (`import.go`, registered from its own `init`, editing no
shared list) and runs on every stack. It drives to Start, because its write is
recorded through the audit seam; a `--dry-run` report writes nothing.

- **It reads the tables the previous set built.** `users` and `memberships`
  exist only in a database carried over from the previous deployment — the
  current set builds neither — and the command reads them by hand. The host
  leaves them in place, and does not drop them until it has confirmed the
  import, because there is only one forward migration and no reverse.
- **It writes through the account store.** A user becomes an account through
  `authn.AccountAdmin.Create`, with the old identifier carried over unchanged so
  the host's own rows that name it keep pointing at the same person, its kind
  `user`, its address written as `import-unverified` — an assertion that is no
  proof of ownership, so no automatic social link and no enterprise merge acts
  on it — and its old password entry handed over in the old envelope for the
  account module to convert to the PHC form without loss. A user switched off in
  the old system becomes a disabled account, switched off the moment it is made.
  A membership becomes a `authn.Memberships.Put` of source `import`, and its
  role a row of `member_roles` through `grantIn`.
- **It is idempotent and resumable.** An account or membership already there is
  left as it is and counted skipped, so a run may be repeated without reverting
  a change made since — a role an administrator raised, a status one set. The
  one half-written row a later run finishes is a membership whose role row an
  earlier run never wrote: the role is filled in and the row counted resumed. A
  row it cannot import — an identifier that breaks the account shape or begins
  with the static prefix, an entry it cannot read, a tenant the roster does not
  hold, an address another account already owns, an identifier whose account was
  deleted after an earlier run imported it — is counted failed and the run goes
  on, and the underlying cause is logged at the failing row.
- **A dry run decides from reads alone**, so it cannot see a failure only the
  write reveals; its created count is an upper bound the real run narrows. What
  it never does is write a row.
- **It reports counts, never content.** The answer carries the counts of
  created, resumed, skipped and failed rows and the reasons of the failures; no
  password, hash or address reaches the answer, the journal or the log.
- **Rollback is by backup.** Only forward migrations exist, so undoing the
  import means restoring the database from the copy taken before it, or running
  the previous binary against the untouched old tables.

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
- The CRM users' and the operator's entries are imported at a low PBKDF2 cost
  by `static.HashPBKDF2`. An imported entry is read and bounded, not judged, and
  the first check that holds re-hashes it to the module's current cost, so a
  case does not depend on the cost. A case whose property rests on how long a
  derivation takes sends a plaintext password through the admin endpoint
  instead, where the module derives at its current cost, as the case of two
  creations of one email at once does.
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
  `signedIn(user)`, `bearer(cookie)`, `operator()`, `basic(user)`,
  `noCredential{}` — and fail the test when the request cannot be made; `h.do`
  returns the error instead, for requests sent from several goroutines at once.
  `signedIn` runs the login flow on the replica the request goes to and sends
  the session cookie the completion set; `bearer` carries a cookie a case holds
  from one replica to another; `basic` is the operator's Basic credential, and a
  case about a CRM user's Basic credential expects the operators' table to
  refuse it. `bearerHeader(value)` sends an access token as
  `Authorization: Bearer`, which is how the token cases reach a guarded route;
  `postForm` sends a form body to a token endpoint, which the JSON-writing
  `post` cannot.
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
