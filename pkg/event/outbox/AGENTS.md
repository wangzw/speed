# pkg/event/outbox

The transactional outbox: an event staged in the transaction of the write it
announces is published on the event bus once that transaction commits, never
when it rolls back, and again and again until the bus accepts it. It is a
capability of its own inside the event directory.

This package is the interface: `Outbox`, whose one method stages an event on a
GORM session, and the two sentinels it refuses a session with. It registers no
module. The implementation is `relay`, which registers `event.outbox.relay`,
keeps the staged events in a table of the delivered database, and relays them.

## Where the authority is

- What this capability is and why it is shaped this way:
  [`docs/design/modules/design-outbox.md`](../../../docs/design/modules/design-outbox.md).
- The decisions behind it: why an announcement is staged in the write's
  transaction and relayed after the commit
  ([`adr-outbox-scope-2026-09-29.md`](../../../docs/adr/adr-outbox-scope-2026-09-29.md)),
  why it lives in the event directory with an interface package of its own
  ([`adr-outbox-packaging-2026-09-29.md`](../../../docs/adr/adr-outbox-packaging-2026-09-29.md)),
  and how the relays of several replicas share one table
  ([`adr-outbox-replicas-2026-09-29.md`](../../../docs/adr/adr-outbox-replicas-2026-09-29.md)).
- The API itself: the godoc here and in `relay`.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **This package imports the event root package and GORM, and nothing else.**
  Every module that stages an event imports it; such a module has both already.
  A driver or a bus's client belongs to `relay`, which a host registers. The gate
  is `TestTheInterfacePackageCostsTheEventPackageAndGORMAlone` in `deps_test.go`.
  The event root package, for its part, never imports this one.
- **The row is written on the caller's session, never on a connection of its
  own.** That is the whole promise: the row commits or rolls back with the
  caller's own statements. A row written through the delivered handle instead
  outlives a rollback, and on SQLite it waits for the write lock the caller's
  transaction holds.
- **`Stage` refuses a session that is not in a transaction, and a transaction
  begun on another connection pool.** Loosening the first lets a caller hand in
  the handle it opened its transaction on — the row commits at once and the
  event goes out whether or not the write does. Loosening the second stages rows
  where no relay of this assembly reads. The pool is compared, not the database
  a connection reaches.
- **Every statement on the table is raw SQL.** The audit capture and the tenant
  filter act on the model path: through a model, every staging, claim and
  removal would leave an audit row, and a claim the capture failed would stop
  the relay. The table holds every tenant's events, which is no business of the
  tenant filter.
- **A relay publishes outside any transaction and holds no connection while it
  does.** The in-process bus runs subscribers inside `Publish`, and they use the
  delivered handle; on a database held to one connection a relay holding it
  would wait for them while they waited for it.
- **A claim is judged by the database's clock, and on PostgreSQL it locks the
  rows it takes and skips the rows another claim is locking.** Judged by each
  replica's clock, a replica whose clock runs ahead takes claims over early;
  without the lock, two claims running at once both take a row; waiting on the
  lock instead of skipping stalls every relay behind the slowest. The cases that
  hold this are `TestTwoRelaysOnOneTablePublishEachStagedEventOnce` and
  `TestAClaimSkipsTheRowsAnotherClaimIsTaking`.
- **A row is handed to the bus only while its claim outlasts one publish.** Past
  that point another relay may be taking it over, and both would publish it.
- **A staging brings the relay's next read forward and never puts it off.** A
  read put off by every staging never comes while stagings keep arriving closer
  together than the burst, and nothing is published until they pause. The case
  that holds this is `TestASteadyStreamOfStagingsNeverStarvesTheRelay`.
- **A refused row is never dropped.** There is no dead letter and no attempt
  limit; the row carries its refusals and the last reason, and waits a growing
  delay. A bus that errs, panics or outlasts the publish bound has refused; the
  refusal gives back only a row the relay still holds. The cases that hold this
  are in `relay/refusal_test.go`.
- **The identifier is assigned through `dispatch.Stamp` and the payload decoded
  through `dispatch.Payload`**, the code every bus implementation uses. A second
  copy here would let a subscriber see one shape from a direct publish and
  another from a staged one.
- **`tableName` is one constant in two places**: `relay/statements.go` and every
  migration file of both dialects.
- **The stop stage gives back every row the relay holds**, due at once, so the
  next relay does not wait for a claim to run out — and no other relay's row:
  one another relay is publishing, made due, is published again by a third.
  The case that holds this is `TestTheStopStageGivesBackTheRelaysOwnRowsAlone`.

## How a module stages an event

A module requires `(*outbox.Outbox)(nil)`, takes it up in `New`, and stages
inside the transaction that makes its write:

```go
err := database.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
	if err := tx.Create(&deal).Error; err != nil {
		return err
	}
	return staged.Stage(tx, event.Event{ID: wonID, Type: "deal.won", TenantID: tenantID, Payload: won})
})
```

The host writes `event.outbox.relay` and a bus implementation into the run-level
inclusions of every command that runs such a module, and imports `relay` so it
registers.

## Running it

From the repository root, over every module in the workspace, `make test` and
`make test-race`; on this capability alone:

```
go -C pkg test ./event/outbox/...
go -C pkg test -race ./event/outbox/...
```

The cases run on SQLite and on PostgreSQL. The PostgreSQL ones reach the shared
container through `pkg/internal/pgpool/pgtest`, and skip on a machine without
docker unless `CI` is set.
