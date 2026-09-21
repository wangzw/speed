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
  why writes do not block the work they record
  ([`adr-audit-failure-2026-09-21.md`](../../docs/adr/adr-audit-failure-2026-09-21.md)).
- The API itself: the godoc in this package and in `journal`.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The root package imports the standard library alone.** It is the package a
  module imports to name the anchor `(*audit.Audit)(nil)` on the middleware
  chain, and Go resolves dependencies per module — so anything added to it lands
  in the dependency list of every module that places a layer against audit.
  Adding a non-standard-library import here is the shape to reject; what needs
  one belongs in `journal`.
- **`tableName` is one constant in two places.** The model's `TableName`, the
  migration files and the statement-level self-reference judgement all have to
  agree. The judgement holds because the module's own write goes through the
  model path, where the target table is resolved; a write issued as raw SQL
  would carry no table and would not reach the plugin at all. Someone changing
  the name changes it in the constant and in both migration files.
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
- **A capture path this module drives never returns its failure to what it was
  recording.** The request layer, the subscription and the plugin report through
  the failure signal and carry on. `Record` is the one that returns an error,
  because its caller asked for the record and can decide what a failure means.
- **The output plugin holds nothing between `Initialize` calls.** The database
  module hands one plugin instance to every handle it builds, and a process
  driving two commands builds two registries that share it. What a callback
  needs is closed over as it is registered — the handle of that call, and a
  failure signal of that handle's own.
- **The layer's `Provides` carries `(*audit.Audit)(nil)`**, the same token the
  descriptor delivers under. That is the only anchor another layer has for
  placing itself inside or outside audit, so the two must not drift.

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
