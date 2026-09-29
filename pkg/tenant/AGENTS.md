# pkg/tenant

The tenancy capability directory: the interface package a model embeds and a
handler reads a tenant from, `pkg/tenant/resolve` — the module that reads a
tenant name off the request surface — and `pkg/tenant/catalog`, the module that
keeps the roster and filters statements to the tenant they carry.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-tenant.md`](../../docs/design/modules/design-tenant.md).
- The decisions behind it, one file per decision:
  [`docs/adr/`](../../docs/adr/) — `adr-tenant-scope-2026-09-21`,
  `adr-tenant-packaging-2026-09-21`, `adr-tenant-resolution-2026-09-21`,
  `adr-tenant-ordering-2026-09-21`, `adr-tenant-identifier-2026-09-21`,
  `adr-tenant-directory-2026-09-21`, `adr-tenant-isolation-2026-09-21`,
  `adr-tenant-scoping-2026-09-21`, `adr-tenant-guarantees-2026-09-21`,
  `adr-tenant-scoping-2026-09-28`, `adr-tenant-scoping-2026-09-29`,
  `adr-tenant-directory-2026-09-29`.
- The API itself: the godoc in this package. Read `Model`'s comment and
  `CrossTenant`'s before writing a scoped model.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **The root package registers no module.** A model embeds `Model` and a
  handler reads an identifier, so a module registered here would put a roster
  and a request layer into the available set of every binary that touches
  either. Registration lives in the subpackages: importing
  `pkg/tenant/catalog` is what decides that a binary has tenants at all, and
  importing `pkg/tenant/resolve` is what decides that its requests are scoped.
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
  statement face. Both facts are in the design's non-guarantees section.
- **`catalog/catalog.go` reaches a handle only through `db.Database`.** The
  plugin registers callbacks; nothing in this module opens a database of its
  own or writes SQL. A table belongs to the module that declares it, and this
  module's one table comes from `catalog/migrations/`.
- **Every statement of the roster's takes its connection of the delivered pool
  through `db.Connection`, with `connectionWait` as the bound, and runs under
  the caller's context** (`catalog/roster.go` `onConnection`). The database
  module owns that bound — the wait for the connection and every lock wait of
  the statements on it, together — and no second one is written here: a bound
  of the roster's own would cover the connection and leave SQLite's lock wait,
  which no context ends, to the busy timeout. The roster's statements never
  join a caller's transaction — its methods take a context, which carries none
  — and on a database held to one connection a caller inside its own
  transaction on the delivered handle holds that connection until its own call
  returns: an in-process subscriber entering a disabled tenant inside its
  publisher's transaction is the ordinary case. Past the bound the call fails
  with `ErrNoConnection` when no connection came, and with the engine's own
  error when a lock did not; neither is a refusal, because answering an entry
  from the snapshot instead would drop work for a tenant the table serves. A new
  statement of the roster's goes through `onConnection`, and the connection a
  write takes is given back before its reread takes one (`catalog/wait_test.go`).
- **`ErrRosterMissing` means the roster table is not there, and nothing else.**
  A statement that fails is followed, on its connection and outside any
  transaction, by a listing of the database's tables (`failedStatement`,
  `tableAbsent`); only a listing without the table gives the sentinel, whose
  repair is the migration command. A lock the bound ran out on, a lost
  connection or a refused permission is reported as it stands. The drivers word
  a missing table each their own way, and this module imports no driver, so the
  question is put to the database rather than to the error text; this
  module's own answers — a conflict, a registration already there, an unknown
  tenant — are not followed by it (`answered`).
- **The roster table does not embed the convention.** It is the fact scoping is
  decided from, so a read of it that needed a tenant first would have nothing to
  read it with. It is not special-cased anywhere: it takes the same path through
  the filter as any table that embeds nothing.
- **`Status`'s zero value is `StatusDisabled`.** A status column never written,
  or holding a value a later build does not know, has to read as not served. A
  change that puts the zero on the serving side inverts the direction a value
  nobody chose falls, and `scope_test.go` fails when it does.
- **The registration window spans the global `Init` stage, not this module's
  own callback.** It opens in `Migrate` and shuts in `Start` for that reason: a
  module registering an extractor declares no dependency on this one, so it may
  legally register from an `Init` that runs first. Opening it in `Init` would
  turn a legal registration into a panic.
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
filter recognises the field by the struct it comes from, not by its name
(`catalog/recognise_test.go`). A base struct of an unexported type is skipped
by GORM altogether, so the convention inside one is not on the model at all.

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

A model that should be scoped and does not embed this reads exactly like a table
that is global on purpose, and nothing says which it is. That is the one failure
this convention cannot catch, and it is why the design writes it as a
non-guarantee rather than pretending to detect it.

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
container through `pkg/internal/pgpool/pgtest`, which skips them on a machine
without docker and fails them under CI. A roster is built over the handle the
database module delivers, from `pkg/internal/dbtest`, because its statements
take their connections through `db.Connection`, which refuses a handle opened
around that module; the filter's own cases build theirs by hand. `pkg/tenant/catalog/plugin_test.go` is where the filter's four
answers are pinned; `pkg/tenant/catalog/reach_test.go` is where the statements
it has to reach beyond the query processor are — a scan, a Row whose refusal
comes back through the row it hands back without taking a connection, an
association join, a subquery rendered into another statement;
`pkg/tenant/catalog/recognise_test.go` is where the models the filter takes for
scoped are, a field taking the convention's column over included;
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
connection holds, bounded whatever the locator's busy timeout and answered as
no missing table; and a connection that cannot be opened, answered as no
missing table either — and `pkg/tenant/catalog/race_test.go` is where a dropped
table is still told apart on PostgreSQL; `pkg/tenant/catalog/readonly_test.go`
is where a read-only model's reads over a nullable tenant column are, on both
dialects;
`pkg/tenant/resolve/extract_test.go` is where the
registration window's behaviour under a duplicate or a late registration is;
`pkg/tenant/resolve/admit_test.go` and `panic_test.go` are where the layer's
answers to a request are — the four statuses, what an admitted request
carries, which logger a refusal goes to, and whose panic the layer recovers;
`pkg/tenant/resolve/config_test.go` is where the judgement over this module's
own configuration is pinned — a missing key, an empty list or name, and an
endpoint named twice; `pkg/tenant/catalog/roster_test.go` is where the
identifier's value domain is refused on the read path. The comparison against
the settings module's restatement of it needs both modules' predicates at once,
so it lives in that module's own external test package,
`pkg/settings/settings_test.go`.
