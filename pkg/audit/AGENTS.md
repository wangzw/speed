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
  ([`adr-audit-failure-2026-09-21.md`](../../docs/adr/adr-audit-failure-2026-09-21.md)).
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
  the name changes it in the constant and in both migration files.
- **The statement callbacks write on the session they are handed.** That is what
  puts the row in the statement's transaction and keeps it off a second
  connection. Writing through a handle captured at `Initialize` instead — or
  through a fresh connection — reopens both problems the choice closed: a
  rolled-back write that still left a row, and a second writer contending on an
  engine that admits one.
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
  statement ran on; nothing has to be remembered between `Initialize` calls, so
  a second handle — including one `db.Open` builds — needs no special care, and
  no per-handle state can leak between registries.
- **The layer's `Provides` carries `(*audit.Audit)(nil)`**, the same token the
  descriptor delivers under. That is the only anchor another layer has for
  placing itself inside or outside audit, so the two must not drift.
- **The plugin is installed on every handle the database module builds, the
  second connection included.** `db.Open` carries the same declared plugins and
  applies no migrations, so on a second connection this module's table is
  whatever that database happens to have: usually absent, in which case every
  captured write fails and only the first failure is reported. The design
  document carries this as a non-guarantee; it is not something to fix by
  guessing which handle is which.
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

The tests bring up their own in-memory database and ask for no service and no
environment: the Go toolchain is the whole of what they need installed.
