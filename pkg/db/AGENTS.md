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
  how a module's own statements take a connection with bounded waits
  ([`adr-db-waits-2026-09-29.md`](../../docs/adr/adr-db-waits-2026-09-29.md)),
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
  plugins.** It holds the pool behind the handle and the engine's
  `Spec.BoundLockWait`, and `Connection` reads it from whatever session it is
  handed — a transaction, a `WithContext`, a connection built with `Open` —
  which is how a plugin holding nothing but a session takes a bounded
  connection of the right pool. A handle without it is one this package did not
  open, and `Connection` refuses it rather than bounding only half the wait.
- **A connection `Connection` bounded goes back to the pool with the lock-wait
  setting it had, or not at all.** A connection handed back carrying the bound
  cuts every later statement's lock wait short on it with nothing reporting
  it, so a setting that cannot be put back closes the connection.
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
		BoundLockWait:       boundLockWait,       // bounds one connection's wait for a lock, and puts it back
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
constraint-refusal, refused-commit and bounded-connection cases each engine runs
on the handle its module delivers.
