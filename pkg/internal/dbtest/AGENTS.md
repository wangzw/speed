# pkg/internal/dbtest

The database capability for other modules' tests, delivered by the engine's own
module: `SQLite` and `Postgres` drive that module through its `New` stage in a
registry of their own, over a configuration the real loader reads, and keep the
handle it opened open until the test ends.

It registers no module and delivers nothing to an assembly. It is a shared
test fixture, neither a capability directory nor a program, and it exists
because a module whose own statements go through `db.Connection` cannot be
tested on a handle it opened itself: `Connection` refuses a handle the database
module did not open, and such a handle carries none of what that module
installs — the engine marker, the constraint classification, the field
encryption — nor the pool shape it gives a locator. The tenant roster, the
settings store and the audit journal are those modules; a copy of this fixture
in each would differ in nothing and drift apart. Its placement follows
[`adr-shared-backend-unit-2026-09-21.md`](../../../docs/adr/adr-shared-backend-unit-2026-09-21.md):
a fixture with no wrapper package of its own is a unit of its own under the
`pkg` module's `internal/`.

## Boundaries

- **No migration is applied, and no plugin is declared.** Only the engine's
  module takes part in the drive; a case builds the tables it needs and
  installs the plugin it tests on the handle it was given.
- **Each call is an assembly of its own.** Two calls on one locator are two
  pools on one database, as two replicas hold them; a connection built with
  `Open` from a delivered handle is what `Database.Open` returns, not a second
  call here.
- **It is reachable only from inside the `pkg` module**, and only from tests:
  nothing but a `_test.go` file imports it.
- **The PostgreSQL locator comes from `pkg/internal/pgpool/pgtest`.** This unit
  starts no server of its own.

## Running it

It is exercised through its callers — `go -C pkg test ./tenant/... ./settings/...
./audit/...` — and needs nothing but the Go toolchain for SQLite, and the docker
CLI for the PostgreSQL cases, which `pgtest` skips on a machine without docker
unless `CI` is set.
