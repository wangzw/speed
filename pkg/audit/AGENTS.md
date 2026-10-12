# pkg/audit

The audit module: the record of who did what, when, and to what. Facts reach it
through four capture paths — the database plugin, the event bus, a layer on the
entry point's middleware chain, and a seam other modules call — and all of them
write into one table this module builds.

The root package carries the interface, the entry type and the capture-path
names. It registers no module and imports the standard library alone. The
implementation is the `journal` subpackage, which registers the module
`audit.journal` and delivers the capability exclusively.

The API is the package's godoc, the root package's and `journal`'s.

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
  that carry no target table to judge. That reach is a non-guarantee, not a
  gap to close here.
- **The impersonation detail keys are the journal's, not the caller's.** Under an
  impersonating identity the journal writes `on_behalf_of`,
  `impersonation_scope` and `session` into the detail from the identity in the
  context, over whatever the caller put under those names, and removes `session`
  when the identity has none. On every other row, with no identity in the context
  included, it removes the caller's keys under those names. A caller's own
  detail, and an event payload field that becomes a detail key, must not use
  them: the value is dropped without an error, so a collision goes unseen.
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
- **A request item is compared with what the engine matched, character for
  character, and this module reads no route syntax of its own.** The layer runs
  inside the engine after the route is matched, takes the method and
  `c.FullPath()` — the path the route was registered under — and looks for the
  pair among the items. A second reading of route syntax would be a second
  parser that can part from the engine's without anything reporting it, and
  asking the engine is the one way to get its answer. A request with no matched
  route has an empty `FullPath` and equals no item: the 404 and the 405 are not
  recorded, and neither is a HEAD request to a route registered for GET. Nothing
  can check an item against the routes an endpoint has, so an item that is off by
  a character never matches, with no error; the only startup failure is an entry
  that is not a method, a space and a path beginning with a slash, which
  `readSettings` refuses.
- **The layer puts nothing in the writer's place.** The outcome is
  `c.Writer.Status()` read after `c.Next()`. The engine's writer already records
  the status and carries `Flush`, `Hijack` and `Unwrap`, so a recorder of this
  module's would only be a second copy of the status that can disagree with the
  first. An informational status the writer still holds when the handler only
  set it — 103, with a body written after — is read as 200, which is what
  `net/http` answers with after sending it; 101 is final and stays. After a
  takeover of the connection the status line is the handler's, on a connection
  the layer cannot read, and the row holds the status the writer held.
- **The layer recovers nothing.** It notes whether the chain returned, and any
  exit that is not a return — a panic, a panic of nil under
  `GODEBUG=panicnil=1`, `runtime.Goexit` — writes the row with the outcome
  `panic` while the unwinding goes on untouched. Catching the value in order to
  throw it again would clear a nil panic and cut a `Goexit` short, and a layer
  that wrote its row only for a value it had caught would leave a handler that
  ended its goroutine without a row.
- **A request whose handler abandons its connection is recorded as a panic.**
  `net/http`'s `ErrAbortHandler` is a handler asking for its connection to be
  dropped, not a defect; the entry point's fallback lets that value past
  without answering, but this layer sits inside it and cannot tell the two
  apart, so the outcome column holds `panic` for a request the caller got no
  response to. Recording the request is what this layer is for, so the row
  stays; the misreport is accepted as a non-guarantee.
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
- **A row that goes out on a connection of its own waits for a connection of
  the delivered pool only within a bound.** On a database held to one
  connection, a caller holding a transaction on the delivered handle holds that
  connection until after its own call returns, so a row that waited for it
  without a bound would wait on the caller for good. The connection is taken
  through `db.Connection`: within `openedRowWait` for a statement's row on a
  connection built with `Open`, and `connectionWait` for the rows this module
  writes itself. Nothing a row can see tells a caller's own hold from another
  caller's brief one, which is why the wait is bounded rather than recognised:
  a bound at zero fails such a write whenever anyone else holds the
  connection, and no bound hangs. The insert's wait for a lock is the engine's
  — SQLite's busy timeout, which no context ends and which a caller's write
  transaction on the same file makes the row wait out, PostgreSQL's context,
  `lock_timeout` or `statement_timeout` — and this module writes no dialect
  code of its own for it.
  Telling which table a connection built with `Open` reaches waits for no lock:
  SQLite's main file is read with `PRAGMA database_list`, which takes none, not
  with its table-valued form, which is a query and waits for a shared lock a
  writer committing keeps from it.
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

Most cases bring up their own SQLite database, on the handle the database
module delivers — from `pkg/internal/dbtest`, or through a whole assembly —
so that the journal meets what an assembly hands it, but for the case that
runs it over a handle opened with `gorm.Open` (`journal/wait_test.go`). The
request-level cases (`journal/request_test.go`) assemble the real entry point
beside the journal and send requests to the port it listens on, because what
they observe — the route a request was dispatched to, the status a client
received, a connection a handler gave up — is the engine's, and a router built
by hand would only show what the case built. The
PostgreSQL cases take a database of their own on the shared fixture in
`pkg/internal/pgpool/pgtest`, which starts a container through the docker CLI:
they skip on a machine without docker, and fail instead where `CI` is set.
