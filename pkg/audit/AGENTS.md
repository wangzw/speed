# pkg/audit

The audit module: the record of who did what, when, and to what. Facts reach it
through four capture paths — the database plugin, the event bus, a layer on the
entry point's middleware chain, and a seam other modules call — and all of them
write into one table this module builds.

The root package carries the interface, the entry type and the capture-path
names. It registers no module and imports the standard library alone. The
implementation is the `journal` subpackage, which registers the module
`audit.journal` and delivers the capability exclusively.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-audit.md`](../../docs/design/modules/design-audit.md).
- The decisions behind it, one file per decision: [`docs/adr/`](../../docs/adr/) —
  what audit does and does not deliver
  ([`adr-audit-scope-2026-09-21.md`](../../docs/adr/adr-audit-scope-2026-09-21.md)),
  why the capture surface has these paths
  ([`adr-audit-capture-2026-09-21.md`](../../docs/adr/adr-audit-capture-2026-09-21.md)),
  why the statement path fails with what it records
  ([`adr-audit-failure-2026-09-21.md`](../../docs/adr/adr-audit-failure-2026-09-21.md)),
  what a host reading the table may rely on
  ([`adr-audit-sink-2026-09-29.md`](../../docs/adr/adr-audit-sink-2026-09-29.md)).
- The API itself: the godoc in this package and in `journal`.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The root package imports the standard library alone.** It is the package a
  module imports to name the anchor `(*audit.Audit)(nil)` on the middleware
  chain, and what it imports belongs to the closure of every package that
  places a layer against audit — so anything added to it lands in the dependency
  list of every module that does. Adding a non-standard-library import here is
  the shape to reject; what needs one belongs in `journal`.
- **`tableName` is one constant in two places.** The model's `TableName`, the
  migration files and the statement-level self-reference judgement all have to
  agree. The judgement holds because the module's own write goes through the
  model path, where the target table is resolved; a write issued as raw SQL
  would carry no table and would not reach the plugin at all. Someone changing
  the name changes it in the constant and in every migration file of both
  dialects.
- **On the delivered connection, the statement callbacks write on the session
  they are handed.** That is what puts the row in the statement's transaction
  and keeps it off a second connection. Writing through a handle captured at
  `Initialize` instead — or through a fresh connection — reopens both problems
  the choice closed: a rolled-back write that still left a row, and a second
  writer contending on an engine that admits one.
- **A statement that has already failed is not recorded.** GORM runs every
  callback on a processor whatever the earlier ones did, and the session the
  row would go out on carries the statement's error, so an insert attempted
  there fails with that same error and dresses the caller's duplicate key or
  plugin refusal up as `ErrWriteFailed`. The row could not stand anyway: the
  transaction is on its way to a rollback, and PostgreSQL refuses every further
  statement in it. Recording failed attempts would need a second connection,
  which writing on the session the callback is handed rules out.
- **The statement callbacks register on the model-path processors only.**
  `Exec` and `Raw` are left out on purpose. Adding them would put raw SQL in
  reach, which is where the module's own migrations and a host's own cleanup
  live, and it would put the self-reference judgement in front of statements
  that carry no target table to judge. That reach is a non-guarantee in the
  design document, not a gap to close here.
- **The masked key set is closed, and every spelling in it is flat.** The log
  module compares a registered name segment by segment against a key path while
  this module compares it against a whole key; restricting the set to spellings
  with no dot in them is what makes the two readings agree on every member. A
  name with a dot in it would be masked in one place and not the other, with
  nothing reporting the difference.
- **A failing write fails different things on different paths, and the split is
  the decision, not an accident.** Statement capture writes on the statement's
  own session, so its failure fails that statement and the record rides the
  statement's transaction — that is what keeps the row and the write it
  describes one thing, and what leaves a single-writer engine nothing to contend
  over. The request layer and the subscription report through the failure signal
  and carry on. `Record` returns an error, because its caller asked for the
  record and can decide what a failure means. Changing one path to match another
  undoes the reason that path has its rule.
- **The plugin holds no handle.** GORM hands each callback the session the
  statement ran on; nothing has to be remembered between `Initialize` calls,
  and no per-handle state can leak between registries.
- **The layer's `Provides` carries `(*audit.Audit)(nil)`**, the same token the
  descriptor delivers under. That is the only anchor another layer has for
  placing itself inside or outside audit, so the two must not drift.
- **A request whose handler abandons its connection is recorded as a panic.**
  `net/http`'s `ErrAbortHandler` is a handler asking for its connection to be
  dropped, not a defect; the entry point's fallback lets that value past
  without answering, but this layer sits inside it and cannot tell the two
  apart, so the outcome column holds `panic` for a request the caller got no
  response to. Recording the request is what this layer is for, so the row
  stays; the design document carries the misreport as a non-guarantee.
- **The plugin is installed on every handle the database module builds, the
  connections built with `Open` included, and a row for a statement on such a
  connection belongs in the delivered database's table.** `Open` carries the
  same declared plugins and applies no migrations, so the database it reaches
  usually has no audit table, and one process keeps one audit table. The
  capture asks `db.OpenedFrom` of the statement's session and, when it answers,
  writes the row through the delivered handle it names: before the statement
  commits, failing the statement when the row cannot be written, but outside
  its transaction. The answer is the database module's to give. Replacing it
  with a guess made here — a process-wide list of running journals, or a probe
  for the table on each handle — hands one run's rows to another's table,
  because the plugin instance is shared by every registry in the process.
- **A connection built with `Open` that reaches the delivered audit table itself
  is the delivered connection for its row**, which goes on the statement's own
  session: through the delivered handle it would wait for the write lock its
  own statement holds on SQLite, and stay behind a rollback on PostgreSQL.
  `reachesDeliveredTable` asks the two engines which table each connection
  reaches — the main database file on SQLite, the server's start time, the
  database and the relation the table's name resolves to on PostgreSQL. Two
  shortcuts break it: comparing locators, which spell one database many ways
  and are the database module's to read; and taking "this connection has an
  audit table" for "this is the delivered one", which a copy or another run's
  database also has.
- **A row that goes out on a connection of its own waits for the delivered
  database only within a bound.** A caller holding a transaction on the
  delivered handle holds that database's write lock — and, on a database held
  to one connection, the connection itself — until after its own call returns,
  so a row that waited for either without a bound would wait on the caller for
  good. The bound is `openedRowWait` for a statement's row on a connection built
  with `Open`, and `connectionWait` on taking a connection for the rows this
  module writes itself. Nothing a row can see tells a caller's own hold from
  another writer's brief one, which is why the wait is bounded rather than
  recognised: a bound at zero fails such a write whenever anyone else is
  writing, and no bound hangs. On SQLite the lock wait happens inside the
  driver and an ending context does not cut it short, so `boundLockWait` sets
  the busy timeout of the connection the row takes and puts it back afterwards;
  a connection whose busy timeout cannot be put back is discarded, never handed
  back to the pool carrying the row's bound. On a SQLite in-memory database in a
  shared cache the driver waits for a table lock with neither the busy timeout
  nor the context ending it, so none of these bounds holds there; the design
  document carries that as a non-guarantee.
- **The event column is unique, and the event path's insert yields on it.** A
  broadcast bus hands every event to every replica, every replica writes into
  the one table, and the event's identifier is the same on every delivery, so
  the first insert writes the row and every later one does nothing. Two
  shortcuts break it without a compile error: an insert that meets the key
  without the conflict clause reports every later delivery through the failure
  signal as a broken table, and an empty identifier stored as a value rather
  than as null merges every event that carries none into the first. The column
  is also what a reader matches a row to the event it records by.
- **The event path's field spellings are this module's own choice.** A payload
  is read for an object and an outcome under a short list of candidate names,
  and one that carries neither leaves those columns empty without a signal. A
  publisher wanting its events recorded reads that list in `event.go`; nothing
  checks the two ends against each other.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this module alone:

```
go -C pkg/audit build ./...
go -C pkg/audit vet ./...
go -C pkg/audit test ./...
go -C pkg/audit test -race ./...
GOWORK=off go -C pkg/audit build ./...   # the standalone leg, without the workspace
```

Most cases bring up their own in-memory database. The PostgreSQL cases take a
database of their own on the shared fixture in `pkg/internal/pgpool/pgtest`,
which starts a container through the docker CLI: they skip on a machine without
docker, and fail instead where `CI` is set.
