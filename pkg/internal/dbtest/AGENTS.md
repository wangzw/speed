# pkg/internal/dbtest

The database capability for other modules' tests, delivered by the engine's own
module: `SQLite` and `Postgres` drive that module through its `New` stage in a
registry of their own, over a configuration the real loader reads, and keep the
handle it opened open until the test ends.

It registers no module and delivers nothing to an assembly. It is a shared
test fixture, neither a capability directory nor a program, and it exists
because a handle a test opens itself is not what an assembly hands a module:
it carries none of what the database module installs — the engine marker, the
constraint classification, the field encryption — nor the pool shape it gives
a locator, such as the one connection of an in-memory database. The tenant
roster, the settings store, the audit journal and the outbox relay are tested
on it, and so are the rate limiter's domain cases, which drive the settings
module over it; a copy of this fixture in each would differ in nothing and
drift apart. Its placement follows
[`adr-shared-backend-unit-2026-09-21.md`](../../../docs/adr/adr-shared-backend-unit-2026-09-21.md):
a fixture with no wrapper package of its own is a unit of its own under the
`pkg` module's `internal/`.

## Boundaries

- **No migration is applied, and no plugin is declared.** Only the engine's
  module takes part in the drive, beside the `config` module the reader is
  supplied to; a case builds the tables it needs and installs the plugin it
  tests on the handle it was given.
- **Each call is an assembly of its own.** Two calls on one locator are two
  pools on one database, as two replicas hold them; a connection built with
  `Open` from a delivered handle is what `Database.Open` returns, not a second
  call here.
- **What a call returns is the engine's own product.** It comes out of that
  engine's `New`, so a test may supply it for the same engine's module in
  another registry and `Migrate` accepts it; an implementation of
  `db.Database` a test writes itself is refused there, and is supplied to a
  placeholder module that declares only the capability instead.
- **It is reachable only from inside the `pkg` module**, and only from tests:
  nothing but a `_test.go` file imports it.
- **The PostgreSQL locator comes from `pkg/internal/pgpool/pgtest`.** This unit
  starts no server of its own.

## Running it

It is exercised through its callers, the packages whose tests import it, which
`grep -rl --include='*_test.go' pkg/internal/dbtest pkg` lists:

```
go -C pkg test ./db/sqlite/... ./tenant/... ./settings/... ./ratelimit/... ./audit/... ./event/outbox/...
```

It needs nothing but the Go toolchain for SQLite, and the docker CLI for the
PostgreSQL cases, which `pgtest` skips on a machine without docker unless `CI`
is set.
