# pkg/db

The database module: it delivers the capability the modules with data take up —
the connection, its pool, the plugin installations, the field encryption and the
migration run — and defines the resources a module declares its migrations and
plugins through.

The root package carries the contract and everything the engines share, and it
wraps GORM rather than abstracting it: the calling surface is `*gorm.DB`, and
nothing here sits on top of it. Concrete engines are subpackages — `postgres`,
`sqlite` — and each registers one module, binds its own driver, and declares the
same capability exclusively.

The API is the package's godoc, the root package's and each subpackage's. Read
`Spec`'s field comments before adding an implementation.

## Boundaries

- **The root package imports no dialect driver.** Its third-party surface is
  GORM and GORM's own closure, plus what reading configuration through
  `pkg/config` brings with it, and no more, so a host that imports this package
  carries no engine but the one it deploys on. Nothing enforces that at compile
  time — an implementation subpackage and the root package are one module, so
  both drivers are in one dependency list — and `TestRootPackageImportsNoDriver`
  in `deps_test.go` is the gate that stands in for it.
- **A driver belongs to the subpackage that binds it.** The root package reaches
  an engine only through the `Spec.Dialector` an implementation hands it.
- **The root package registers no module of its own.** Each engine's subpackage
  does it in its own `init`, so a host that imports an engine has it available
  as soon as a command names it on its tree. The command has to name it: the
  capability is delivered exclusively and the dependency closure does not pull
  an exclusive provider in on its own, so leaving every engine out of the tree
  is how an assembly runs without a database. The root package does import
  `pkg/log`, whose initialisation registers the module named `log`, so a binary
  that imports it carries that registered module, which takes part where a
  command tree names it and on entries that take every registered module.
- **The stance fails the startup; it does not stand the module down.** A named
  implementation whose locator is absent or blank returns an error naming the
  key, because a module taken in at run level has to start. `StateDisabled` is
  not something an implementation states — resolution gives it, and only to a
  provider that stands down over an exclusive claim.
- **A new engine is a subpackage, a `Dialect` value, and a namespace of its
  own.** The dialect value is also the name of the subdirectory the engine's
  migrations are read from, so it has to stay a single path element. The
  namespace must be one of its own: the configuration manifest is collected from
  every registered module, so two implementations declaring items on one path
  conflict as soon as a host imports both — a conflict the collection raises on
  its own, which resolution never gets a chance to clear.
- **A dialect carries migrations only if it meets all three admission
  conditions**, because the engine-neutral migration suite holds them on every
  engine it runs on: DDL goes inside a transaction, a migration file of several
  statements is run by the driver's own path for that without this module
  splitting SQL text, and an engine that appears in multi-replica deployments
  supplies a cross-process mutex. The first two are properties of the driver and
  are observed there; the third is `Spec.NewMigrationLock`.
- **Migrations are declared by the module that owns the tables.** This module
  applies them and writes none of its own: a table belongs to whoever declares
  it, and the record table is the one exception because reading it is how a run
  learns what is already applied.
- **The sentinel table is closed at five entries and there is no sixth.**
  Encryption and decryption failures are run-time errors that belong to the
  statement that hit them and get no sentinel here; a new class of startup
  failure is a decision, not an addition, and `ErrForeignInstance` is one that
  was decided. A constraint refusal is a run-time
  error too, and it is classified with GORM's own `gorm.ErrDuplicatedKey` and
  `gorm.ErrForeignKeyViolated`, attached beside the driver's error rather than
  in its place.
- **`Migrate` accepts only an instance this module's own `New` built.** A host
  may supply an instance for the module, and neither `Prepare` nor `New` then
  runs; the plugins and the encryption serializer are installed in `New`, so a
  handle that did not come through it has none of them, and nothing later would
  say so. `ownProduct` judges the origin — the product type and the module name
  it carries, not what its handle carries — before the configuration is read,
  the mutex is taken or a statement is issued, and refuses anything else with
  `ErrForeignInstance`: the host's own implementation of `Database`, a test's
  stand-in, another implementation's product. A host that wants to supply a
  database builds it with this module's own `New` in a registry of its own.
  `Close` is the other direction: it releases the pool behind whatever instance
  reports a handle with one through `Database`, a refused one included, because
  the rollback after the refusal reaches it and nothing else releases it once
  the drive has begun. An instance that gives it no pool to release — a typed
  nil pointer, one whose `DB` panics, one whose handle has no pool to reach or
  panics when asked for it — is left alone: a pool the host's own code holds
  behind it is the host's, and a panic or an error out of the cleanup would
  replace or bury the error the rollback is carrying. A test that needs a
  stand-in for the capability supplies it to a placeholder module that declares
  only `db.Database`, since a drive that reaches `Migrate` refuses it for an
  engine.
- **The mark `Open` installs lives in the connection's plugin table, ahead of
  the declared plugins.** It names the delivered handle, and `OpenedFrom` reads
  it. The plugin table is the one piece of a handle every derived session — a
  `WithContext`, a `Session`, a transaction — shares by reference; a context
  value is gone at the first `WithContext` and a statement setting at the first
  new statement, and `OpenedFrom` answering false there sends a plugin's row to
  a database no migration reached, with nothing reporting it. Installing it
  after the declared plugins leaves their `Initialize` unable to ask.
- **The engine marker every handle carries lives in its plugin table too,
  installed by `openHandle` before either caller installs the declared
  plugins.** It holds the engine's dialect, which is how a callback holding
  nothing but a statement reads the statement's identifiers by that engine's
  rules (`namesColumn`).
- **`Connection` bounds the wait for a connection, and nothing else.** It takes
  a connection of the pool the handle it is handed is on — this package's or a
  host's own, a `ConnPool` that is a `*sql.DB` or names one through
  `gorm.GetDBConnector` — and runs `fn` on it under the caller's context; a
  handle already on one connection (a transaction, a pinned session), or on a
  wrapper naming no pool, runs `fn` on itself (`pooledOn`). It reads, sets and
  restores no lock timeout: a statement in `fn`
  waits for a lock as its engine makes it wait, for the connection's busy
  timeout on SQLite and until the context, `lock_timeout` or
  `statement_timeout` ends it on PostgreSQL. A wrapper that reset a timeout
  before a statement would cost a round trip on every audited row and would
  put a statement ahead of `ROLLBACK TO SAVEPOINT` inside a transaction
  PostgreSQL has aborted.
- **What `fn` leaves open on the connection never holds its release up.** The
  session is pinned to a wrapper of the connection (`pinnedConn`) that records
  each transaction begun on it and ties each result set to the call's end:
  when `fn` returns or panics, the transactions still open are rolled back on
  the connection and the result sets closed, then the connection goes back.
  Left to `database/sql`, a transaction under a context that never ends holds
  the connection's close for good, and one it rolls back when a context ends
  is rolled back on a goroutine of its own that asks for the connection to be
  dropped — on `:memory:`, the database with it — racing the release.
- **The SQLite subpackage refuses a locator in shared-cache mode**, delivered or
  opened: its `Dialector` fails to initialise (`sharesCache`). The driver waits
  for a shared-cache table lock through `sqlite3_unlock_notify`, which neither
  the busy timeout nor a context ends. The locator is read as the driver hands
  it to SQLite and as SQLite parses it (`readLocator`, `sqliteURI`) — the URI
  ending at a `#`, `%HH` decoded in the name and the parameters, an escaped NUL
  ending the part it is in, an empty parameter name skipped, the last of a
  repeated parameter taken, the VFS the URI or the driver names, the driver's
  as a C string ending at a NUL — and so is the one `connectionScoped` reads; a
  reading that differs from SQLite's opens a shape nothing bounds, or a private
  database under a pool. The cases in `scope_test.go` hold both readings to
  what two real connections observe.
- **The SQLite subpackage begins every transaction itself**
  (`transactionConn.BeginTx`, `beginStatement`): IMMEDIATE, unless the
  locator's `_txlock` names another mode; DEFERRED for one begun read-only, and
  on a `mode=ro` locator naming no mode. Under SQLite's DEFERRED default a transaction that
  read first is refused the write lock at once under any concurrent writer,
  and the driver's own begin drops `_txlock` whenever the locator gives
  `_time_format`. `transactions_test.go` holds the
  mode to the lock a second connection observes.
- **A transaction runs on every locator whose connections cannot write.**
  SQLite begins an IMMEDIATE transaction on a database it opened read-only
  (`mode=ro`, `immutable=1`, a file the process may not write) as a read
  transaction, but refuses it outright on a connection whose `query_only` is
  on; `BeginTx` begins DEFERRED there, whatever mode the locator names, and
  asks the connection only when SQLite refused the BEGIN, since a statement can
  turn `query_only` on for one connection of the pool. A connection that does
  not answer is taken for one whose `query_only` is off, so the refusal it gave
  stays the caller's error. Reading `query_only` out of the locator instead
  misses every spelling of the pragma and every connection a statement changed.
  `readonly_test.go` runs a transaction on each read-only shape, and
  `transactions_test.go` holds the fallback to every answer a connection can
  give.
- **The GORM logger every handle carries writes through the log module, and
  writes no value a statement binds or reads** (`statementLogger`). It writes
  through the `*slog.Logger` its
  module's `New` took up (`Spec.logger`): the log capability's `Named` logger,
  so that every record carries the module's own name, `module=db.sqlite` or
  `module=db.postgres`, or, when the assembly delivers none, the log package's
  process default logger with that attribute stamped on it. It is never read
  from a statement's context (`log.FromContext`): a context given attributes and
  no logger answers with a logger derived from the process default one, which
  the log package's public interface cannot tell from an injected logger, and an
  injected logger's `module` is its injector's. The guarantees of no bound or read value, no error
  text and no `Scan` text are those of a statement's record. GORM's own
  messages, `gorm info`, `gorm warning` and `gorm error`, are GORM's text with
  its data formatted in, `detail`: the error text GORM reports and, for a model
  the migrator cannot parse, the model value the caller handed over, with the
  reason. Where a record goes, in what format, from which level and with what
  masked is that logger's configuration and none of this module's to decide: an
  empty output list writes none, and a
  level above Info writes no statement of a `Debug` session, because GORM's
  Info, Warn and Error are written at slog's levels of those names and those
  statements are Info records. The messages, levels and attributes of the
  records are a fixed vocabulary the cases pin, because
  operators search the output for them and `internal/dbhost` counts the
  failed statements' records on a replica's output. The messages are
  fixed words and whatever varies is an attribute — the statement is
  `statement`, GORM's own text `detail` — because the log module's redaction
  judges the attributes of a record and not its message. The logger answers
  GORM's `ParamsFilter` with no values, so the text GORM hands `Trace` keeps its
  placeholders, and it writes a failed statement's error classes
  (`failureClasses`) in place of its error, whose text can quote a value. The
  guarantee does not rest on that redaction, whose rule set starts empty and
  which masks by a key or by a shape someone registered: a value GORM has filled
  into a statement's text is under no key and has no such shape.
  These groups of cases hold it. The logging case in `internal/dbsuite` runs on
  both engines in a copy of the test binary whose assembly has no log module, so
  that its records are the process default logger's and arrive on standard
  output, and reads the copy's two streams apart. Each statement it holds is
  written as exactly one record, with the level and message this module fixes,
  the engine's module name and a call site in the file that ran it; a
  statement that succeeds outside a `Debug` session is not written as a
  `statement` record; standard error carries no record of the logger; and
  neither stream carries a value any statement binds or reads. `logger_test.go`
  drives the logger over a recording handler and holds every record it writes
  to that vocabulary — its level, its message, the attributes
  of its kind and no others — and a failure's classes to their spelling.
  `logrouting_test.go` runs the module under the real log module writing JSON
  to a file and holds a statement that fails, one in a `Debug` session, a slow
  one and one that fails through `Scan` to one record each, from the delivered
  handle and from one built with `Open`, and what the log configuration decides
  — a level of warn or error, an empty `outputs`, a redaction rule someone
  registered — to the records it governs.
- **A statement run through GORM's `Scan` finisher, `(*gorm.DB).Scan`, is
  written without its text.** The finisher swaps the handle's logger for GORM's
  recorder, whose `ParamsFilter` hands every value back, and gives the real
  logger the finished text through `Trace` alone, so the values are in it before
  this logger is asked anything: the dialect renders the text with its values
  whether or not a record is then written, and the record that is written drops
  the text. `Trace` recognises that call by its caller's
  run-time name (`scanFinisher`) and, when the finisher handed it a text, writes
  `withheldStatement` as the record's `statement` in the text's place, keeping
  the classes, the time, the rows and the call site; a `Scan` whose statement
  was never built, its handle having carried an error before the chain built
  one, hands over an empty text, and its record has no `statement`. Nothing else
  is recognised: `Row`, `Rows` and the generics API's `Scan` (`gorm.G[T]`, which
  runs through `Find`) swap no logger, and their statements are written with
  their placeholders. The recognition happens inside `Trace` and sets no GORM
  package variable, `logger.RecorderParamsFilter` included: those are shared by
  every handle in the process, a host's own among them. A GORM that renames the
  finisher leaves its statements written with their values, and nothing but the
  logging cases reports it. A logger put in front of this one is outside the
  guarantee: one that embeds `logger.Interface` without exposing
  `ParamsFilter`, as the logger GORM's migrator installs under `DryRun`
  (`printSQLLogger`) does and a host's `Session(&gorm.Session{Logger: …})`
  wrapper may, makes GORM fill the values into every statement it relays, and
  one that relays `Trace` from a method of its own is the caller `Trace` sees in
  the finisher's place; nothing in this repository installs either. The logging
  case in `internal/dbsuite` issues a refused `Scan` statement and one in a
  `Debug` session and holds both to the withheld text in place of the statement
  and the first to the classes of its refusal, GORM's sentinel and the engine's
  own code, which each engine's test hands in; `logger_test.go` holds each
  record of this path to its call site, its classes and the withheld text, with
  a `Row` statement as the control, a `Scan` that built no statement to a record
  without one, and the package to leaving `logger.RecorderParamsFilter` as GORM
  has it.
- **Some routes of GORM write without any handle's logger, and so ignore the
  log configuration and the redaction.** The schema parser reports a model it
  cannot parse, and a hook whose signature is mistyped, through the
  package-level `logger.Default`; the recorder GORM puts in the handle's place
  while `DB.Scan` runs hands its `Warn` and `Error` to `logger.Default` too (it
  writes from Warn up, so an `Info` is dropped there); and the migrator under `DryRun` prints the statements it would run with
  `fmt.Println`. These reach standard output through the standard library,
  outside `statementLogger`, so this logger is not the only way GORM writes
  there, and nothing here replaces or silences them: `logger.Default` is shared
  by every handle in the process, a host's own among them.
- **The logger judges a record before it builds one, and each method GORM calls
  finds the call site itself.** Each of them judges GORM's own level, then asks
  the held logger whether it writes the record's level, then reads the call
  site, and `Trace` calls `fc` for the statement last, so a record that is
  filtered out renders no SQL, except that a `Scan` has the dialect render its
  text, values included, before the logger is asked, and drops it (the boundary
  before this one). The level question is this logger's to ask: the
  record goes to the handler directly, and the log module's level layer does not
  judge a level again in `Handle`, so a record built without asking is written
  whatever level the log configuration sets. Every record carries the call site
  GORM's own logger reports — the first frame outside GORM's source — as its
  program counter, which a handler that adds the source to its output reads, and
  as the attribute `caller`, because the chain the log module assembles never
  adds the source. `utils.CallerFrame` skips the frame it is called from, so it
  is asked in the body of the method GORM calls and never in a helper: a helper
  would be the frame, and every record would name `logger.go`. `write` builds
  the record with `slog.NewRecord` over that frame's program counter plus one and
  hands it to the held logger's handler, because the logger's own methods would
  stamp the file of the call that made them. A frame's counter is that of the
  call instruction, while the counter a record carries is the return address
  `runtime.Callers` reports, one past it, and a handler resolving it with
  `runtime.CallersFrames` steps back one. The handler's error is dropped, and a panic in the handler chain
  behind the held logger is not caught: nothing else that writes a record through the
  same logger catches one. The context GORM hands over goes to the handler as it
  is, a nil one replaced by `context.Background()`. `logger_test.go`
  holds the order with a recording handler that, like the log module's level
  layer, does not judge a level in `Handle`: the held logger is asked only about
  what GORM's own level lets through, a record it will not write is neither
  rendered nor handed over, and one it will write is rendered after the
  question and before the delivery. It holds every record's program counter,
  resolved as a handler resolves it, to the line of the call that caused the
  record in `logger_test.go`, its `caller` to that same line, and neither to
  `logger.go`.
- **The requirement on the log capability is optional and stays declared, and
  every handle shares the logger `New` took up.** `NewModule` requires
  `*log.Logger` with `Optional` set, and neither half of that is to be dropped.
  Optional, because the database has to work in an assembly that never took
  logging in: the log module delivers `Logger` exclusively, so the closure does
  not take it in on the engine's behalf, and a hard requirement fails a command
  that names the engine and not the log module with `ErrMissingProvider` — the
  example hosts' `migrate` commands are such commands. Declared, because an
  assembly that did take it in has to have it constructed first: without the
  edge nothing orders `db.*` and `log`, ties go by module name, both engines
  sort ahead of `log`, and `New` looks for a logger before the log module has an
  instance. The lookup is then answered `ErrMissingProvider`, which is also how
  an absence reads, so `New` takes the fallback and the records of a host that
  configured logging go to the standard output of the process default logger,
  with nothing to say they should have gone elsewhere. Only that answer is an
  absence: `Spec.logger` returns any other failure of the lookup, two providers
  of the capability among them, with the module's name and `%w`, and the startup
  fails, and so does a provider whose `Named` gives no logger, with the
  module's name. The logger is taken up in `New`: `Prepare` runs before any module is
  constructed, so a lookup there finds only an instance a host supplied. The
  product keeps the logger, and `Open` hands it to the connection it builds
  rather than asking again, so a query moved to another database is written
  where the delivered handle's are, under the same module name; a product a host
  built with this module's own `New` in a registry of its own and supplied keeps
  that registry's logger, not the one the run it is supplied to delivers. The
  logger is mandatory: `newStatementLogger` panics on a nil one, which
  `logger_test.go` holds, and `openHandle` is handed one by both callers. A
  database that loses the edge still starts and still writes, only somewhere
  else, so nothing but the cases reports it. The descriptor cases hold
  `Requires` to exactly that one optional requirement, on the descriptor a
  `Spec` builds and on each engine's own module; `depsorder_test.go` holds the
  order `log`, the database module, then a module that requires the database;
  and `logrouting_test.go` holds the rest through a log provider the registry
  constructs, whose name sorts after the database's, because a supplied
  instance exists before any `New` runs and shows no order: the provider
  receives a failed statement's record under the database's own module name,
  `Named` is called once, a failed statement on a handle built with `Open` is
  written to the same logger, and two providers of the capability fail the
  startup with `ErrAmbiguousProvider`. The fallback is held twice: a host with
  no log module starts and delivers a working handle, and, in a copy of the
  test binary, writes its statements to standard output as one line each,
  stamped with the module's name, as does the logging case in
  `internal/dbsuite` on each engine.
- **Every wrap uses `%w`.** A `%v` leaves the text all but identical and makes
  the sentinel unreachable, which is the failure the note in `errors.go` exists
  to prevent; the error-chain cases hold each path to it.

## How an engine contributes

A subpackage binds its driver, fixes its namespace, and registers the module this
package builds from its `Spec`. The connection, the plugins, the encryption, the
statement logging, the migration run and the release of the pool all stay here,
shared with every other implementation, so that no engine drifts from its
neighbour in the behaviour a host sees. The requirement on the log capability is
declared by `NewModule` for every engine, and no `Spec` field carries a logger.

```go
func init() { core.ProcessRegistry.Register(Module()) }

func Module() core.Module {
	return db.NewModule(db.Spec{
		ModuleName:          ModuleName,          // "db.sqlite"
		ConfigNamespace:     ModuleName,          // where this engine's items are mounted
		Dialect:             Dialect,             // also the migration subdirectory
		Dialector:           Dialector,           // the driver, and the only place it appears
		ConstraintViolation: constraintViolation, // the driver's unique and foreign key refusals
		// ConnectionScoped only for an engine with locators private to one
		// connection; NewMigrationLock and DefaultMigrationLockTimeout only for an engine
		// that appears in multi-replica deployments.
	})
}
```

The `ModuleName` the subpackage exports is also the name a host gives `Supply`
for an instance of this engine. The products of both engines are one type,
defined here, so a host that imports both names the module to say which of them
an instance belongs to.

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
go -C pkg/db build ./...
go -C pkg/db vet ./...
go -C pkg/db test ./...
go -C pkg/db test -race ./...
GOWORK=off go -C pkg/db build ./...   # the standalone leg, without the workspace
```

The SQLite cases need nothing beyond the Go toolchain: that driver is pure Go.
The PostgreSQL cases run against a container — `postgres:17-alpine`, named
`speed-pgtest`, started by the fixture on first use and left running, with a
database of its own per case. A machine without docker skips them, and under
`CI` that skip becomes a failure: those cases hold the gate on the cross-process
migration mutex, and a gate that silently skips where it is meant to run is not a
gate. `internal/dbhost` is the test host the multi-process cases build and run;
`pkg/internal/pgpool/pgtest` is the container fixture, shared with the key-value and
event modules' postgres backends; `internal/dbsuite` holds the
engine-neutral migration cases each engine's fixture is driven through, and the
constraint-refusal, refused-commit, concurrent read-then-write, connection and
logging cases each engine runs on the handle its module delivers. The logging
case runs its statements in a copy of the test binary, which is how it reads
standard output apart from standard error; the white-box cases of the logger are
in `logger_test.go`, and the ones that run the module under the real log module
in `logrouting_test.go`. `pkg/internal/dbtest` delivers this module's
capability to other modules' tests, for the ones that exercise what an assembly
hands them.
