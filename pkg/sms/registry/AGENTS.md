# pkg/sms/registry

The template registry module, registered as `sms.registry`: it holds the set of
SMS templates in force, decides which one a message goes out under, and manages
the ones this process owns. It delivers one capability, `sms.Registry`,
exclusively.

The root package is both the interface package and the implementation, the way
`pkg/settings` is: there is no implementation subpackage, because the storage is
the database alone.

The API is the package's godoc, and the capability's is the godoc of
`sms.Registry` in the root package.

## The two sources

A template reaches the set in force from one of two places, and the two have
different owners:

- **The configuration source** is the module's own `templates` input item, a
  `map[string]sms.TemplateSpec`. It is read once at startup and frozen into an
  `sms.Declaration`. It is **read-only**: nothing at runtime may remove it,
  change it or disable it, it is never polled, and its review state is not
  tracked — the operator who wrote it has already had the vendor approve it. A
  management call naming a configuration template is `ErrImmutable`.
- **The runtime source** is this module's own `sms_templates` table. These are
  the templates the process registers, hands to the vendor, enables and removes
  while it runs. Each carries the vendor's review state **per language** — a
  vendor approves a template per language, so a logical name approved in one
  language and still under review in another is the ordinary case — which the
  poller reads back.

`Resolve` tries the configuration source first and delegates a configured
message to its declaration, which holds the structural checks; a runtime message
is checked against the row's parameter list and code table, against the review
state **of the message's own language**. `Declared` reports the configuration
templates and the runtime ones that are enabled and approved in every language
they name. `List` reports both, configured ones marked `SourceConfig` and always
enabled with no review tracked, runtime ones carrying their per-language review
and bookkeeping.

## Boundaries

- **The runtime snapshot is replaced as a whole, never written in place, and the
  replacement is serialised.** `registryStore.snapshot` is an
  `atomic.Pointer[snapshot]` and `reloadMu` holds the read-then-swap of
  `swap` together. Several goroutines reload — a writer's, the subscription's and
  the poller's — and without the lock a reload whose read returned an older state
  could swap after one that read a newer state, silently undoing a write the
  replica already reported as done.
- **Before the first load there is no snapshot, not an empty one.** The store is
  built in `New` and the table is first read in `Init`, so a call in between is
  `ErrNotLoaded` — the state is unavailable, never an empty set and never a
  refusal. A snapshot seeded empty would answer every read as though no runtime
  template existed while the table holds something else. The one exception is
  `Declared`, which has no error to return: it reports the configuration
  templates alone until the load, so a consumer reading runtime templates reads
  after this module's `Init`.
- **A write is conditional and reports itself through the affected rows.**
  `Register` and `Offer` both land their codes through one merge path `upsert`:
  it reads the row, merges into it and writes it back inside a transaction, the
  write conditional on the version it read (`WHERE name = ? AND version = ?`), so
  two concurrent writers cannot overwrite each other — a write that matched no
  row means the row changed and the merge is retried (`upsertAttempts`, a bounded
  number). A name the row does not hold is inserted with `ON CONFLICT (name) DO
  NOTHING RETURNING version`, so two concurrent inserts of one name leave one row;
  a name it does hold is merged, and **the languages already there are left
  untouched, their review states included**. A merge on a name whose parameters
  differ is `ErrInvalidConfig` (a logical name describes one message shape); a
  language the name already holds is `ErrTemplateExists` naming it.
  `SetEnabled` updates `WHERE ... enabled <> ?`, so a call whose target state
  equals the current one matches nothing and succeeds without a version bump or
  an event; `Remove` treats a delete that touched no row as `ErrNoTemplate`.
- **Offer calls the vendor before it writes.** A vendor call that fails leaves no
  row, so this process never holds a template the vendor did not accept and could
  never settle. The submission's shape is validated before the vendor call, and a
  language the name already holds, or a shape that disagrees with the one the name
  already describes, is refused before it too, so none reaches the vendor. A new
  language for a name already held is a legal merge and does reach the vendor.
- **A successful write reloads this replica's snapshot and publishes a change
  event.** The reload is the replica's own read of what it just wrote; the event
  (`sms.registry.changed`) tells the others to reload. Neither failing undoes the
  write. The event is an acceleration and never a correctness dependency: the
  poller's ticker converges a replica the event never reached.
- **The poller reads the vendor per language, only for the languages still
  pending**, and a vendor call that fails leaves that language's state as it is —
  recording a state the vendor did not answer would refuse a template it holds,
  or worse, approve one it does not — while a sibling language that could be read
  is still written back. Each pending language is read on its own and its verdict
  written back on its own, so a language approved in one release does not wait for
  another still running; an untracked state is never polled. A verdict equal to
  the stored one writes nothing, so a review still running does not churn the row
  each interval. The poller starts in `Init` (the table is ready by then, where
  `New` would find it missing) and its first pass runs at once. A zero interval
  turns it off.
- **The subscription is made in `Init` and nowhere else.** A bus freezes its
  subscription table when it starts, and the handler does one thing — reload the
  snapshot — which is safe to run at any time, including on the publisher's own
  goroutine and for this replica's own events.
- **It reaches the vendor only through `sms.Provider` and never imports a
  carrier.** `pkg/sms/aliyun` and `pkg/sms/tencent` are not in this package's
  imports: importing one would close a cycle through the root package and break
  the carrier seal. This module takes up `(*sms.Provider)(nil)` and
  `(*db.Database)(nil)` as hard requirements, delivers `(*sms.Registry)(nil)`
  exclusively, takes the configuration reader up from what the host supplies, and
  takes the event bus and the logging capability optionally.
- **The table name is one constant in three places.** `tableName`, the two
  migration files and the statements all have to agree. The statements here are
  native SQL, so nothing resolves the name for them the way the model path would.
  The migration files carry no semicolon in their comments: the statement
  splitter severs a comment at one, which turns the tail into a syntax error.
- **It is deliberately not built on `pkg/settings`.** The settings key set is
  fixed at assembly time — an operator cannot add an undeclared key without a
  release — and a container value never enters the settings tree at all, so a set
  of templates that grows and shrinks at runtime is the case a declarative store
  cannot express. This module holds its own table instead.

## Configuration

This module's input items are mounted under its own namespace, `sms.registry`
(the module name). It accepts two:

- **`templates`** (a map of template name to `sms.TemplateSpec`): the
  configuration source — the templates the operator wrote and had the vendor
  approve. It is a container leaf, so it takes the primary source alone. An
  empty map is legal: a registry whose templates all arrive at runtime declares
  none.
- **`poll-interval`** (a duration, default one minute): how often the background
  pass reloads the runtime set and reads the vendor's review state for the
  templates still pending. Zero accepts convergence through change events alone.
  A negative value is `sms.ErrInvalidConfig`.

The configuration templates are checked with `sms.NewDeclaration` at startup: a
template with no parameter, a repeated parameter name or a language with no code
is `sms.ErrInvalidConfig` naming the template.

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
go -C pkg build ./sms/registry
go -C pkg test ./sms/registry
go -C pkg test -race ./sms/registry
env -C pkg golangci-lint run ./sms/registry/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

Most cases bring up their own SQLite database through `pkg/internal/dbtest`,
which delivers the handle the way an assembly does, and build the table from this
module's own migration file. The cases that also run against PostgreSQL take a
database on the shared container `pkg/internal/pgpool/pgtest` starts and skip on a
machine without docker. The concurrency cases hold the pool to one connection so
the writes serialise at the pool and their assertions are deterministic. No case
needs a real carrier: a fake `sms.Provider` scripts the offer and review answers.
