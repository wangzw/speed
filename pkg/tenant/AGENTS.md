# pkg/tenant

The tenancy capability directory: the interface package a model embeds and a
handler reads a tenant from, `pkg/tenant/resolve` — the module that reads a
tenant name off the request surface — `pkg/tenant/catalog`, the module that
keeps the roster and filters statements to the tenant they carry —
`pkg/tenant/admission`, the module that decides whether an identified caller may
enter the tenant its request resolved to — the common modules that own the
account's own space and the self-service creation of a business organization,
`pkg/tenant/personal` and `pkg/tenant/selfcreate`, and `pkg/tenant/ownership`,
the module that performs the owner transfer and the organization deletion
across the roster, the policy store and the membership store in one
transaction.

The API is the package's godoc. Read `Model`'s comment and `CrossTenant`'s
before writing a scoped model.

## Boundaries

- **The root package registers no module.** A model embeds `Model` and a
  handler reads an identifier, so a module registered here would put a roster
  and a request layer into the available set of every binary that touches
  either. Registration lives in the subpackages: importing
  `pkg/tenant/catalog` is what decides that a binary has tenants at all, and
  importing `pkg/tenant/resolve` is what decides that its requests are scoped.
- **`Admission`'s marker method is exported, and that is not a style choice.**
  `Admission` is `interface{ AdmissionLayer() }`, defined here and delivered by
  the admission subpackage's type. A type of another package cannot implement an
  interface whose method is unexported, so an unexported marker — the shape
  `pkg/ratelimit`'s `Limiter` uses, where interface and implementation share a
  package and the outside embeds it — would fail the registry's delivery check
  for the one implementation there is. The interface is an ordering anchor and
  not a surface: a layer that has to sit inside admission, an authorization layer
  above all, names `(*tenant.Admission)(nil)` in its `After` and calls nothing on
  it, and importing this root package for the anchor needs nothing else of it.
- **The identifier's injection point is closed.** `internal/tenantctx` is where
  a tenant is written into a context, and the compiler is what keeps every
  other package out of it. Exporting a writer — or moving the key into this
  package — removes the enforcement without removing the claim, and the claim
  is the whole point: a tenant is written into a context only after the roster
  has been read. There are two writers and both read it first: the resolving
  layer, once per request, and the roster's two entries, `Catalog.Enter` and
  `Catalog.EnterRegistered`, for a path with no request behind it
  (`catalog/enter_test.go`). A third writer, or one that skips the
  roster, puts into a context a tenant the roster may not carry or serve, and
  the filter, which reads no roster, would act on it. Both writers go through
  `tenantctx.Bind`, which binds the identifier on the context's logger too —
  replacing one bound before — and leaves beside it the way to take it off,
  which `CrossTenant` uses.
- **The root package and `internal/tenantctx` import no module of this
  repository, the logging module included.** A package that defines a model
  imports the root package, and the logging module brings the configuration's
  whole dependency set with it. The logger is reached through the logging
  module's own functions, which the writers hand to `tenantctx.Bind` in a
  `tenantctx.Logger`; `CrossTenant` calls the one `Bind` left in the context.
  Importing `pkg/log` there to call it directly is the shape to reject.
- **`CrossTenant` is the only way across tenants, and it marks rather than
  scopes.** A statement naming a tenant column value other than its context's
  is refused rather than honoured
  (`ErrExplicitTenantMismatch`, in `catalog/plugin.go`). Honouring it would be a
  second way in, and one that carries no mark of intent: a write copied from
  elsewhere and edited would land in another tenant with nothing said. What
  counts as naming the column is whatever the database would take an
  assignment's target for — a key GORM resolves through the schema, and one it
  leaves for the database to resolve: another case, its own quotes, a raw
  column with blanks around it, the primary key placeholder — in an explicit
  SET and in the destination alike (GORM renders the second when it yields
  anything), a struct of another type, a field — the model's own or another
  type's — whose tag carries another spelling of the column, whatever a
  selection names it by and whether its value comes from the caller or from
  GORM's update-time stamp, every one of several assignments reaching the
  column rather than the first, an upsert's
  assignments (`catalog/targets_test.go`). The reading is the widest of the dialects', and a
  raw target that is not one column name is refused under any context: reading
  too narrowly is a row carried into another tenant. What is judged is what
  GORM writes: a field repeating a column exactly that GORM never writes is not
  judged, because refusing it refuses an insert that lands in the context's
  tenant. A value the filter cannot
  read as the context's identifier is refused like another tenant's. The mark
  itself lifts the condition and the fill, not the column: a write under it that
  leaves the tenant column empty or null is refused, because that row would
  belong to no tenant, and so is an insert under any context whose row GORM
  builds without the tenant column at all — a selection or an omission leaving
  it out, or a field bound to it that GORM does not write on insert.
  `plugin_test.go` and `catalog/writes_test.go` pin both halves.
- **The statement filter holds no state, and reads no roster.** The plugin
  instance is frozen with the descriptor at init time, database hands that one
  instance to every handle it builds, and a process driving two commands builds
  two registries that share it — so anything learned per run is the other
  registry's value on the second run. It also does not consult the roster, which
  is why disabling a tenant takes effect on the request face and not on the
  statement face. Both are non-guarantees.
- **The roster's transaction-aware writes join the caller's transaction.** The
  `Catalog` writes open a connection and a transaction of their own, which is
  what a host command or a registration route has. `catalog.TxCatalog` is the
  form for a caller that already holds a transaction — an account's creation,
  where a user account's personal tenant is written in the account's
  transaction — and its methods take that `*gorm.DB` and write through it and
  nothing else. `GetTx` and `SetOwnerTx` join `CreateTx` and `DeleteTx`; the
  ownership module's transfer and deletion use them. They return the whole-roster reread as a closure for after
  the caller's commit: a reread inside the transaction would see no write on
  another connection, and on a database held to one connection would wait for
  the transaction that called it. The methods are delivered as a capability of
  their own beside `Catalog`, exclusively, by the same roster.
- **`catalog/catalog.go` reaches a handle only through `db.Database`.** The
  plugin registers callbacks; nothing in this module opens a database of its
  own or writes SQL. A table belongs to the module that declares it, and this
  module's one table comes from `catalog/migrations/`.
- **Every statement of the roster's takes its connection of the delivered pool
  through `db.Connection`, with `connectionWait` as the bound, and runs under
  the caller's context** (`catalog/roster.go` `onConnection`). The bound is on
  the wait for the connection alone; a statement's wait for a lock is the
  engine's — SQLite's busy timeout, PostgreSQL's context, `lock_timeout` or
  `statement_timeout` — and this module sets none of them. The roster's
  statements never join a caller's transaction — its methods take a context,
  which carries none — and on a database held to one connection a caller
  inside its own transaction on the delivered handle holds that connection
  until its own call returns: an in-process subscriber entering a disabled
  tenant inside its publisher's transaction is the ordinary case. Past the
  bound the call fails with `ErrNoConnection`, and a lock the engine gave up on
  fails it with the engine's own error; neither is a refusal, because answering
  an entry from the snapshot instead would drop work for a tenant the table
  serves. A new statement of the roster's goes through `onConnection`, and the
  connection a write takes is given back before its reread takes one
  (`catalog/wait_test.go`).
- **`ErrRosterMissing` means the roster table is not where the roster's
  statements look for it, or has not been read yet.** A statement that fails is
  followed, on its connection and outside any transaction, by a question to the
  database whether it has the table (`failedStatement`, `tableAbsent`); only an
  answer that it has none gives the sentinel, whose repair is the migration
  command. A roster whose snapshot is not published yet — a call reaching it
  before this module's `Init` — is refused with it as well (`current`). On
  PostgreSQL the question reads the system catalog for the name in the schemas
  the session's search path names, a schema the role holds no USAGE on
  included, and in the ones PostgreSQL searches without their being named
  (`namedSchemas`, `searchPath`): resolving the name as a statement does skips
  a schema the role may not use, and the information schema leaves out the
  tables a role may not read, so either would answer a refused permission with
  the migration. A lock the engine gave up on, a lost connection, a refused
  permission — on the table or on its schema — and a question that fails too
  are reported as they stand. The drivers word
  a missing table each their own way, and this module imports no driver, so the
  question is put to the database rather than to the error text; this
  module's own answers — a conflict, a registration already there, an unknown
  tenant — are not followed by it (`answered`).
- **GORM's association mode is filtered as any statement is, and a join table
  by the tenant the association's keys put into it** (`catalog/associations.go`).
  The association mode writes the whole key of the owner and of the records it
  is given into a statement's conditions before any callback runs, always as an
  IN over a tuple of the key's columns, each qualified by the table's name, so
  the tenant half a caller leaves unwritten is filled there from the context
  (`keyHalves`) — in a tuple over a whole key of the statement's own table,
  named that way (`wholeKey`, `keyColumns`), with another part of it written,
  and nowhere else — and a half naming another tenant refuses a write. **A
  condition is judged by its shape, never by who wrote it**: the shape filled is
  the tuple above — a whole key of the statement's own table, every column named
  that way — and it is filled whoever wrote it, a caller's hand-written tuple of
  that shape included, because nothing in a statement says who wrote one. Every
  other shape — an equality, an IN over the column alone, a tuple over columns
  that are no key or are named otherwise, whatever it is written through — is
  the caller's, and is left as written. Filling one of those is the shape to
  reject: GORM's key and the caller's condition sit side by side in the WHERE,
  and a caller's `tenant_id` that came out empty would turn a write matching
  nothing into one over the whole tenant, past the refusal of an unconditioned
  write, since that condition counts as the caller's
  (`catalog/provenance_test.go`). A column qualified by another table is that
  table's (`ownTable`). A join table's tenant column is the column a
  scoped side's key puts its tenant on (`joinTenantField`), found through the
  relationships GORM hangs on the join table's schema (`joinedBy`); the read that
  joins it into the other side by its table name is judged off its ON
  (`fromJoins`). Taking the join model's own convention for the tenant column is
  the shape to reject: GORM builds a join model of its own that embeds nothing,
  and the read joining by name never sees the model. Two tenant columns, or a
  scoped side and none, is refused rather than filtered twice or passed as
  global. That read carries nothing of the owner, so where the other side is
  global and the owner's key puts no identifier into its ON — joined by the
  rest of its key, or through a tenant field that is not an `ID` — the owner is
  refused on every statement (`unjudgedRead`). The read itself, on an owner no
  statement goes through, is a non-guarantee; reaching GORM's unexported schema
  cache to judge it is the shape to reject.
- **The roster table does not embed the convention.** It is the fact scoping is
  decided from, so a read of it that needed a tenant first would have nothing to
  read it with. It is not special-cased anywhere: it takes the same path through
  the filter as any table that embeds nothing.
- **`Status`'s zero value is `StatusDisabled`.** A status column never written,
  or holding a value a later build does not know, has to read as not served. A
  change that puts the zero on the serving side inverts the direction a value
  nobody chose falls, and `scope_test.go` fails when it does.
- **`WriteGuard` and `Provisioner` are host seams, not capabilities.** The root
  package names them so a host can supply one per process and the management
  surface can require it; no module of this directory delivers either, and the
  roster never consults them. Which tenants an operator may not disable or
  delete is a host policy, and creating a tenant with its first tenant-admin is
  a cross-capability write the host owns. `SetStatusByAccount` is the account
  lifecycle's path and must keep disabling a personal tenant whose account is
  disabled, so the guard belongs to the management surface's operator route
  alone; `protection_test.go` scans the roster's identifiers so a check placed
  "once for every status write" fails there.
- **Self-service creation is a common module, not a host module.**
  `pkg/tenant/selfcreate` owns the whole `POST /api/v1/orgs` decision: the
  caller's eligibility, the alias and profile shape, the two account bounds,
  the creation lock, the audit row and the hand-off to `tenant.Provisioner`. A
  host assembles it with the switch and the dependencies; a host that
  reimplements the decisions has forked the product. The registry name is
  `org.create`, not the package path's `tenant.selfcreate`, because the feature
  document requires a feature's first segment to equal the declaring module's
  and the feature name is the compatibility surface a frontend gates on. The
  two numeric limits are a `settings.Schema` that is not tenant-facing, so the
  tenant settings surface neither lists nor writes `org.create.*`.
  `selfcreate/AGENTS.md` carries the rest.
- **`Kind` is answered, not recorded.** The roster stores no kind column and the
  root package derives none from an identifier, a name or a permission set.
  `KindResolver` is the host seam: the caller tenant read takes it up
  optionally, passes `KindOf`'s answer through, and reads every tenant as
  `KindBusiness` when it is absent. The personal-tenant module
  (`pkg/tenant/personal`) provides the resolver rather than requiring one, and
  an assembly that names that module gets it; an assembly without the module has
  no personal tenant and never sees a resolver. A resolver error is a
  failure of the read (503), never a row silently read as business.
- **The registration window spans the global `Init` stage, not this module's
  own callback.** It opens in `Migrate` and shuts in `Start` for that reason: a
  module registering an extractor declares no dependency on this one, so it may
  legally register from an `Init` that runs first. Opening it in `Init` would
  turn a legal registration into a panic.
- **The callbacks act on this module's own product and on nothing else.** The
  roster and the resolver each recognise the one type they build
  (`productOf` in `catalog/catalog.go` and `resolve/resolve.go`), and an
  instance of any other type is a host's own, supplied for the module: no
  snapshot is loaded for it, no window opened and no layer registered, and what
  it holds is released by whoever supplied it. Taking whatever carries the
  product's methods for the product is the shape to reject: a wrapper a host
  writes around the product carries every one of them, and the callbacks would
  act on the product inside it (`catalog/supplied_test.go`,
  `resolve/supplied_test.go`).
- **The resolution layer refuses through the entry point's `Fail`, with the
  status as the only member of the `Problem`.** A request that carries no
  tenant, names none on the roster or names a disabled one gets the same 404,
  and a roster that cannot be read or a panic of the layer's own gets a 500; the
  shape of the body is the entry point's, and the reason goes to the request's
  logger. A member beyond the status — the reason above all — would let a caller
  tell a name that never existed from one that was switched off.
- **The resolution layer does not read the route.** It runs for every request on
  the endpoint, including one that matches no route and one whose method the
  path does not allow: a request that carries a tenant goes on to the entry
  point's own 404 or 405, and one that carries none is refused by this layer
  before the entry point could tell the two apart. A branch on `c.FullPath()`
  would let those requests reach the layers inside with no tenant.
- **A tenant-scoped route's path tenant is held to the resolved tenant by a
  route-level guard, not an endpoint layer.** `pkg/tenant/pathguard` resolves the
  `tenant` path segment through the roster — alias and identifier, refusing a
  segment that names none or two — and compares identifiers, so the alias and
  the identifier both address the same tenant. `resolve.Module` delivers it
  as the `pkg/http` `RouteGuard` capability; `pkg/authn/mount` applies it to every
  `ScopeTenants` registration and refuses one when no guard is delivered. The
  members, invitations and SSO groups manage the system tenant itself and are
  addressed by the platform domain legitimately, so the shared guard does not
  refuse it. `resolve.Module` also delivers `pathguard.NewTenantAdmin` as the
  stricter `TenantAdminRouteGuard` capability — the same identity rule plus the
  refusal of the platform domain — and the two tenant management modules,
  `authz.tenantadmin` and `settings.tenantadmin`, wrap their own routes with it.
  A platform route that names a tenant as its data is not wrapped, even on the
  same endpoint. `pathguard/pathguard_test.go`, `resolve/path_test.go` and the
  mounter's scope tests hold the rule.
- **The layer recovers the panic of its own work and not the handler's.** An
  extractor registered through the seam runs inside `admit`, which recovers and
  answers 500 under this module's name; `handle` calls `c.Next` outside it, so a
  panic of a route behind the layer reaches the entry point's fallback
  (`resolve/panic_test.go`).
- **The endpoint list is a set, judged at startup.** The entry point's surface
  takes an endpoint by name and appends the layer without deduplicating, so a
  name written twice would put the resolution layer on that endpoint twice and
  run the roster lookup and the identifier binding twice per request. A repeat
  is refused in `moduleConfig.resolve`, naming the endpoint, rather than
  dropped in passing.
- **`ID` refuses to be bound outside its domain.** `ID.Value` answers an
  empty identifier with `ErrNoTenantInContext` and a blank or control character
  with `ErrInvalidTenant`, so an unfilled tenant column fails at the driver on
  any handle, filtered or not — the NOT NULL a migration declares stops a null,
  not the empty string an unfilled column holds. It hands the string back
  beside the error, because GORM keys rows by their primary values and drops
  the error while doing so; a nil value there panics. `scope_test.go` and
  `catalog/unfiltered_test.go` pin both halves, the second on both dialects.
- **The identifier's value domain is stated once here and restated by its
  consumers.** `IsIdentifier` in this package is the module's one statement of
  the rule — non-empty, free of blanks and control characters — and the roster's
  field check (`checkField`) calls it; the empty value is judged just before
  that call, only to pick which of the two messages the failure carries, so the
  domain itself still has one statement. A module that layers a value by an
  identifier restates the rule rather than importing it, because the dependency
  between it and this module is deliberately absent; `pkg/settings`'s
  `ForTenant` is the one that exists. Two statements drift, and the drift is not
  quiet: the rate-limit layer hands `ForTenant` the identifier it read out of
  the request context, so a consumer-side tightening turns every request of an
  accepted tenant into a panic the entry module answers with a 500.
  `pkg/settings/settings_test.go` drives one table of identifier shapes through
  both predicates, so a divergence fails there.

## Shape of a scoped model

A scoped model embeds `tenant.Model`. That one act declares the model and
supplies the column; there is no second thing to remember and no interface to
implement. Embedding counts however GORM flattens it — directly, through an
exported base struct, by pointer, or in a field tagged `embedded` — and the
tenant column is the column GORM parsed the convention's field onto, which an
`embeddedPrefix` renames. The filter finds that field by the struct it comes
from, never by its name or by the column's name (`catalog/recognise_test.go`):
a prefix moves the column off `tenant_id`, and a field of the model's own named
`TenantID` takes the name. Two declarations are refused on every statement,
under any context, with `ErrNoTenantInContext` rather than read as a global
table: the convention embedded where GORM does not read it — through an
unexported struct or field, or a field tagged `-` — which leaves the column
off the model, and the convention embedded twice on two columns.

A field the model declares on the convention's column — named like the
convention's, named otherwise and tagged with the column, or reached first
through another embedding — takes the column over: GORM binds a column to one
field and reads and writes that one alone. The model stays scoped, and the
filter fills and judges the field GORM binds, never the convention's field it
no longer writes (`catalog/recognise_test.go`). Finding the convention by its
field name is the shape to reject: the name then finds the other field, and the
model reads as a global table. The key half and the refusal to be bound empty
come with the convention's field, so a field that takes the column over carries
them only when it is tagged `primaryKey` and typed `ID`.

```go
type Order struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}
```

**The tenant column is part of the primary key, and that is what keeps one
tenant's write out of another's row.** A unique constraint that does not lead
with it — a globally generated identifier, a business key unique on its own —
lets the insert a `Save` or an `OnConflict` produces conflict on the other
tenant's row and update that row into the writer's tenant. Nothing in the
statement names another tenant, so the filter has nothing to refuse; the shape
of the key is what closes it. Marking a model's own key `primaryKey` beside this
one is what gives the composite key a scoped table has, and the migration has to
declare the same one — which is the obligation the filter cannot check, along
with the column's type and its NOT NULL.

A write addressed by a struct's key gets the tenant half of the key from the
context wherever the rest of the key is written, except through `Save`: GORM
decides before any callback that a struct with a zero key part is a new row,
so saving one whose tenant half is unwritten inserts it and, against a row
that exists, fails on the key. No hook runs before that decision, so the filter
cannot fill the half in time. A write meant to create a row or rewrite the one
its key names is an upsert on the key —
`Clauses(clause.OnConflict{UpdateAll: true}).Create(&x)` — which leaves the
tenant half to the filter and conflicts on the context tenant's own row; a save
that must stay a save writes the tenant half itself, from `FromContext`
(`catalog/writes_test.go`).

A many-to-many association between scoped models names one tenant column for
both keys in its join table, and needs no join model of its own for it:

```go
Contacts []Contact `gorm:"many2many:deal_contacts;joinForeignKey:TenantID,DealID;joinReferences:TenantID,ContactID"`
```

Without the two tags GORM gives each side a tenant column of its own in the join
table, and every statement through it is refused. A many-to-many association
with a global model joins by the whole key too, `joinForeignKey:TenantID,DealID`,
and needs the field bound to the tenant column to be an `ID`: otherwise its read
cannot be held to a tenant, and the model declaring it is refused on every
statement. An association by a foreign
key carrying the tenant column — a has-many by `foreignKey:TenantID,DealID` —
reads and adds within the tenant, and its removal is refused: GORM takes a
record off its owner by writing a null into every column of the foreign key,
the tenant column among them. The unscoped removal, which deletes the record,
stays in the tenant (`catalog/associations_test.go`).

A model that should be scoped and does not embed this reads exactly like a table
that is global on purpose, and nothing says which it is. That is the one failure
this convention cannot catch, and it is why this is a non-guarantee rather than
something the filter pretends to detect.

**A model that embeds the convention only to read, by tenant, a table another
module declares and writes carries neither obligation above** — the audit table
is one such table. The tenant-led key and the NOT NULL bind the side that
creates the table and writes it, because what they keep out is a write. Through
such a model a tenant's read never reaches a row whose tenant column is null; a
read across tenants does, and reads that column into the convention's field as
the empty identifier with no error, so a reader that has to tell those rows
apart uses a tenant field that can hold a null — on a type that does not embed
the convention, or as a pointer field of the read-only model's own that takes
the column over, which a tenant's read still scopes (`catalog/readonly_test.go`). **Such a model issues reads
only.** A write through it is filled and checked, but that table's key does not
lead with the tenant column, so an upsert by its key can reach another tenant's
row, and the table is its declaring module's to write. The filter cannot tell a
read-only model from a scoped one; review holds this.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

This module on its own:

```
go -C pkg/tenant build ./...
go -C pkg/tenant test ./...
go -C pkg/tenant test -race ./...
```

The tests bring up their own database, except the cases that prove a refusal
on the PostgreSQL driver as well: those take a database on the shared
server the compose stack brings up, through `pkg/internal/pgpool/pgtest`; those
cases carry the `integration` tag, so a default run compiles none of them. A
roster is built over the handle the
database module delivers, from `pkg/internal/dbtest`, so that it meets what an
assembly hands it, and once over a handle opened with `gorm.Open`
(`catalog/handles_test.go`); the filter's own cases build theirs by hand. `pkg/tenant/catalog/plugin_test.go` is where the filter's four
answers are pinned; `pkg/tenant/catalog/reach_test.go` is where the statements
it has to reach beyond the query processor are — a scan, a Row whose refusal
comes back through the row it hands back without taking a connection, an
association join, a subquery rendered into another statement;
`pkg/tenant/catalog/recognise_test.go` is where every declaration shape of the
convention is pinned in one matrix on both dialects — the shapes scoped, a
prefixed column and a field taking the column over included, the shapes
refused, and the lookalikes left global — and where a join is recognised as
the model it reaches; `pkg/tenant/catalog/associations_test.go` is where every
shape of a many-to-many association is pinned the same way — its join table
scoped or refused — with each
statement of the association mode on an owner and records addressed by the rest
of their keys, and where an association by the whole key, a deletion of
links between loaded records, a replacement keeping records GORM did not save,
an owner refused for an association whose read cannot be held to a tenant, and
the read itself, which stays global, are;
`pkg/tenant/catalog/provenance_test.go` is where every shape a caller
writes a condition on the tenant column in is judged as written, on both
dialects, beside the one shape filled, a tuple over the whole key qualified by
the statement's table's name, whoever wrote it;
`pkg/tenant/catalog/targets_test.go`
is where an assignment's target is read the way each database reads it, on both
dialects; `pkg/tenant/catalog/create_test.go` is where a registration that must
not overwrite is refused against the table rather than the snapshot, where
a write's reread outlives its caller, and where reads of the table publish in
the order they read; `pkg/tenant/catalog/enter_test.go` is where entering a
tenant is — a refusal taken from the table rather than the snapshot, what an
entered context carries, the entry that admits a disabled tenant, and a table
dropped under an entry or a write; `pkg/tenant/catalog/wait_test.go` is where
the roster's waits are — for a connection, bounded inside a caller's
transaction on a database held to one connection, taken when it comes free
inside the bound, and ended by the caller's context; for a lock another
connection holds, the locator's busy timeout for each statement and answered
as no missing table; and a connection that cannot be opened, answered as no
missing table either — and `pkg/tenant/catalog/race_test.go` is where a dropped
table is still told apart on PostgreSQL, and `catalog/handles_test.go` where a
refused permission — on the table or on its schema — and a lost connection are
not taken for one, where the table is looked for in the schemas the search path
names, and where the search path is read as PostgreSQL reads it;
`pkg/tenant/catalog/quiet_test.go` is where the roster's own answers are
followed by no question to the table; `pkg/tenant/catalog/readonly_test.go`
is where a read-only model's reads over a nullable tenant column are, on both
dialects;
`pkg/tenant/resolve/extract_test.go` is where the
registration window's behaviour under a duplicate or a late registration is;
`pkg/tenant/resolve/admit_test.go`, `unmatched_test.go` and `panic_test.go` are
where the layer's answers to a request are — each refusal and the 500, in the
entry point's error shape or a delivered responder's, what an admitted request
carries, which logger a refusal goes to, the requests that match no route or
whose method is not allowed, and whose panic the layer recovers. They run the
entry point module itself on a loopback port the system picks, and read the
response off the socket (`resolve/host_test.go`);
`pkg/tenant/resolve/config_test.go` is where the judgement over this module's
own configuration is pinned — a missing key, an empty list or name, and an
endpoint named twice; `pkg/tenant/catalog/roster_test.go` is where the
identifier's value domain is refused on the read path. The comparison against
the settings module's restatement of it needs both modules' predicates at once,
so it lives in that module's own external test package,
`pkg/settings/settings_test.go`.
