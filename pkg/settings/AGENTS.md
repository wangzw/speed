# pkg/settings

The settings module: the runtime-variable parameters. A module declares the
setting items it reads, an operator writes values per tenant or globally while
the process runs, and a consumer reads them back out of an in-memory snapshot
under the declared type. Storage is the database, the value model is
declarative and typed, and a read falls back tenant row, global row, declared
default.

The root package is both the interface package and the implementation, unlike
`pkg/tenant` and `pkg/audit`: it registers one module and delivers the settings
surface exclusively. Storage is the database alone, so there is no
implementation subpackage to hold one backend apart from another.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-settings.md`](../../docs/design/modules/design-settings.md).
- The decisions behind it, one file per decision:
  [`docs/adr/`](../../docs/adr/) — what the capability directory delivers
  ([`adr-settings-scope-2026-09-21.md`](../../docs/adr/adr-settings-scope-2026-09-21.md)),
  why items are declared rather than typed at the call site
  ([`adr-settings-declaration-2026-09-21.md`](../../docs/adr/adr-settings-declaration-2026-09-21.md)),
  why a read has three layers and how a layer is named
  ([`adr-settings-layer-2026-09-21.md`](../../docs/adr/adr-settings-layer-2026-09-21.md)),
  how a change reaches the other replicas
  ([`adr-settings-propagation-2026-09-21.md`](../../docs/adr/adr-settings-propagation-2026-09-21.md)),
  and where the history of a change lives
  ([`adr-settings-audit-2026-09-21.md`](../../docs/adr/adr-settings-audit-2026-09-21.md)).
- The API itself: the godoc in this package.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **A layer is named by a constructor, never by a value.** `Global()` and
  `ForTenant(id)` are the whole of how a read or a write says which layer it
  acts on; `Layer`'s unexported method closes the set of implementations to
  this package, and `ForTenant` refuses an identifier outside the tenant
  identifier's value domain — non-empty, and carrying no blank or control
  character anywhere in it. No length is part of that domain: which width the
  column has is the declaring party's to say, in its own migration. The zero
  value is a mistake at the call site and panics, and that check is a run-time
  one — a call site that passes no layer at all still compiles. The empty tenant
  column is storage's spelling of the global row and stays storage's: `tenantOf`
  is the single crossing between the interface and the column, and a second
  crossing is a second way for "no tenant" to come to mean "global". That value
  domain is the tenant directory's, restated here rather than imported — this
  module's code declares no dependency on `pkg/tenant` — so the two statements
  are held in step by a shared table driven through both predicates in
  `pkg/settings/settings_test.go`, this module's external test package, where
  importing the tenant module's `IsIdentifier` is the direction the dependency
  rules allow. A divergence is not quiet: a consumer hands `ForTenant` the
  identifier it read out of the request context, and one outside the domain
  panics into the entry module's recovery layer.
- **The key rule is a second implementation of configuration's, and it has to
  stay one.** `keys.go` derives a key path by the same rules `pkg/config` uses
  — the `config` tag, then the `json` tag, then the field name in lower kebab
  case, embedded structs inlined, `-` skipping — because a parameter moving
  between the two modules must keep its key. Nothing in `pkg/config` is
  exported that could be reused, so the rules live in two places and a change
  to either one has to be made in both. The two differ in what a leaf may be:
  configuration accepts containers and pointers to scalars, this module's type
  set is closed and does not. Configuration only ever decodes, so a type that
  decodes itself from text is a leaf there; here it is usable only when it also
  encodes itself back, because a row is the text the declared type writes, and
  `leaf` refuses one that cannot, together with a default that does not
  survive the round trip.
- **The write discipline is a second statement of configuration's value
  discipline, and it has to stay one.** `coerce` takes what a configuration
  source would give for the same item — duration text and never a bare number,
  a whole float into an integer item, text through a type's own
  `UnmarshalText` — because a parameter moving between the two modules has to
  accept the same values in both, and nothing in `pkg/config` exports those
  rules either. `TestTheWriteDisciplineAgreesWithConfiguration` drives one
  table of YAML values through configuration's real loading path and through
  this module's encoder, so a rule changed on one side and not the other fails
  there.
- **A pointer leaf is refused, and that is deliberate.** Settings answer "was
  this ever written" through the layers, so the third state a pointer would
  express — set to nothing — has no reader. `classifyLeaf` refusing it is what
  keeps the default, the snapshot value and the target's element type one type
  rather than three states of one.
- **The table name is one constant in three places.** `tableName`, the two
  migration files and the absent-table judgement all have to agree. The
  statements here are native SQL, so nothing resolves the name for them the way
  the model path would; someone renaming it renames it in the constant and in
  both dialect files.
- **`changeFor` is the only place a change payload is built.** The sensitive
  rule is a property of the payload rather than of a call site: an event may
  reach a message backend and every subscriber on it, and a credential does not
  earn that reach by being audited. A second place building a payload is a
  second place that can forget to withhold the values.
- **`Delete` does not consult the key table, and that has one consequence.**
  It clears rows whose declaration is already gone, which is why it cannot
  refuse an undeclared key. When the key is still declared its sensitive marker
  is honoured; when it is not, nothing says whether the declaration that is
  gone marked the value sensitive, and the payload reports it as an ordinary
  one rather than guessing. The marker belongs to a declaration, and a declaration that no
  longer exists cannot mark anything.
- **The subscription is made in `Init` and nowhere else.** A bus freezes its
  subscription table when it starts and panics on a later `Subscribe`, and the
  bus interface offers no way to unsubscribe. The handler therefore does one
  thing — replace the snapshot — and is safe to run at any time, including on
  the publisher's own goroutine and including for this replica's own events.
- **`ErrTableMissing` is judged from the driver's text.** The database module
  states there is no fifth sentinel, so there is no typed error for an absent
  table to catch. Only that one failure is classified here; a connection
  failure or a permission problem passes through as its implementation named
  it, so that the fixing action does not point at the wrong cause.
- **The snapshot is replaced, never written in place, and the replacement is
  serialised.** Several goroutines reload — a writer's, the subscription's,
  the ticker's and the lifecycle driver's for the startup loads in `Init` and
  `Start` — and each reload is a read of the table
  followed by a swap. `reloadMu` holds those two steps together: without it, a
  reload whose read returned an older state can swap after one that read a
  newer state, and the replica that just wrote goes back to serving the value
  from before its write. A change that mutated the map in place would be
  visible to a reader halfway through, which is what taking the pointer once
  avoids.
- **Before the first load there is no snapshot, not an empty one.** The store
  is delivered at `New` and the table is first read in `Init`, so a module
  initialised ahead of this one can reach it in between. An empty snapshot
  there would answer every read with its declared default whatever the table
  holds; holding none is what lets `Get` refuse the read with `ErrNotLoaded`
  instead. A change that seeded the store with an empty snapshot would bring
  the silent default back.
- **A read hands out its own copy.** What a read resolves to is either the
  key table's declared default or the snapshot's stored value, and both are the
  module's own storage. A declared type that is a slice underneath — `net.IP`
  is the shape, and the closed type set admits it because it carries its own
  text form — would otherwise be shared with the caller, who could write into
  the declaration or into the snapshot through what it read.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

This module on its own:

```
go -C pkg/settings build ./...
go -C pkg/settings test ./...
go -C pkg/settings test -race ./...
GOWORK=off go -C pkg/settings build ./...   # the standalone leg, without the workspace
```

Most cases bring up their own in-memory SQLite database and ask for no
service. The cases that run the statements against PostgreSQL take a database
of their own on the shared container `pkg/internal/pgpool/pgtest` starts:
they skip on a machine without docker, and fail instead where `CI` is set. The
event bus the cases use is a stub in `helpers_test.go` rather than the memory
module, whose bus constructor is unexported.
