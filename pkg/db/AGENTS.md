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

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-db.md`](../../docs/design/modules/design-db.md).
- The decisions behind it, one file per decision: [`docs/adr/`](../../docs/adr/) —
  what the module takes on
  ([`adr-db-scope-2026-09-14.md`](../../docs/adr/adr-db-scope-2026-09-14.md)),
  why several engines deliver one capability
  ([`adr-db-multiplicity-2026-09-14.md`](../../docs/adr/adr-db-multiplicity-2026-09-14.md)),
  how migrations are applied
  ([`adr-db-migration-2026-09-14.md`](../../docs/adr/adr-db-migration-2026-09-14.md)),
  how a constraint refusal is recognised
  ([`adr-db-errors-2026-09-28.md`](../../docs/adr/adr-db-errors-2026-09-28.md)),
  how a module's own statements take a connection within a bounded wait
  ([`adr-db-waits-2026-09-29.md`](../../docs/adr/adr-db-waits-2026-09-29.md)),
  why the GORM logger a handle carries writes no statement's values
  ([`adr-db-logging-2026-09-29.md`](../../docs/adr/adr-db-logging-2026-09-29.md)),
  why this package's interface exposes GORM
  ([`adr-module-packaging-2026-09-14.md`](../../docs/adr/adr-module-packaging-2026-09-14.md)),
  and where a driver is allowed to live
  ([`adr-module-packaging-2026-09-22.md`](../../docs/adr/adr-module-packaging-2026-09-22.md)).
- The API itself: the godoc in this package and in each subpackage. Read `Spec`'s
  field comments before adding an implementation.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The root package imports no dialect driver.** Its third-party surface is
  GORM and GORM's own closure, plus what reading configuration through
  `pkg/config` brings with it, and no more, so a host that imports this package
  carries no engine but the one it deploys on. Nothing enforces that at compile
  time — an implementation subpackage and the root package are one module, so
  both drivers are in one dependency list — and `TestRootPackageImportsNoDriver`
  in `deps_test.go` is the gate that stands in for it. The cost of having no
  compiler behind this is recorded in `adr-module-packaging-2026-09-22`.
- **A driver belongs to the subpackage that binds it.** The root package reaches
  an engine only through the `Spec.Dialector` an implementation hands it.
- **The root package registers no module.** Each engine's subpackage does it in
  its own `init`, so a host that imports an engine has it available as soon as a
  command names it on its tree. The command has to name it: the capability is
  delivered exclusively and the dependency closure does not pull an exclusive
  provider in on its own, so leaving every engine out of the tree is how an
  assembly runs without a database.
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
- **The sentinel table is closed at four entries and there is no fifth.**
  Encryption and decryption failures are run-time errors that belong to the
  statement that hit them and get no sentinel here; a new class of startup
  failure is a decision, not an addition. A constraint refusal is a run-time
  error too, and it is classified with GORM's own `gorm.ErrDuplicatedKey` and
  `gorm.ErrForeignKeyViolated`, attached beside the driver's error rather than
  in its place (`adr-db-errors-2026-09-28`).
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
  PostgreSQL has aborted (`adr-db-waits-2026-09-29`).
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
  `_time_format` (`adr-db-waits-2026-09-29`). `transactions_test.go` holds the
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
- **The GORM logger every handle carries writes no value a statement binds or
  reads** (`statementLogger`, `adr-db-logging-2026-09-29`). It answers GORM's
  `ParamsFilter` with no values, so the text GORM hands `Trace` keeps its
  placeholders, and it writes a failed statement's error classes
  (`failureClasses`) instead of its text, which can quote a value. A logger
  wrapping GORM's own to change what it writes reports its own file as every
  line's call site, since GORM names the first frame outside its source; each
  method here asks `utils.FileWithLineNum` itself for that reason. The logging
  case in `internal/dbsuite` reads the two streams of a child process apart on
  both engines, and holds every statement to standard output.
- **Every wrap uses `%w`.** A `%v` leaves the text all but identical and makes
  the sentinel unreachable, which is the failure the note in `errors.go` exists
  to prevent; the error-chain cases hold each path to it.

## How an engine contributes

A subpackage binds its driver, fixes its namespace, and registers the module this
package builds from its `Spec`. The connection, the plugins, the encryption, the
migration run and the release of the pool all stay here, shared with every other
implementation, so that no engine drifts from its neighbour in the behaviour a
host sees.

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
logging cases each engine runs on the handle its module delivers. `pkg/internal/dbtest` delivers this module's
capability to other modules' tests, for the ones that exercise what an assembly
hands them.
