# pkg/internal/pgpool

The PostgreSQL connection pool that the key-value and event modules' postgres
backends share. It registers no module and delivers no capability: it wraps the
pool construction — parsing the connection string, carrying the defaults, and
refusing a size the pool cannot take — so that the two backends do not each
carry a copy of it.

## Where the authority is

- Why a shared unit that registers nothing exists at all, and what sharing one
  costs:
  [`adr-shared-backend-unit-2026-09-21.md`](../../../docs/adr/adr-shared-backend-unit-2026-09-21.md).
- What each backend is and why it is shaped that way:
  [`docs/design/modules/design-kv.md`](../../../docs/design/modules/design-kv.md)
  and
  [`docs/design/modules/design-event.md`](../../../docs/design/modules/design-event.md).

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **No module is registered here and no capability is delivered.** The backends
  do both, each under its own namespace and with its own `Config` mount.
- **The pool type is the interface.** `Connect` hands back a `*pgxpool.Pool`,
  not an abstraction of this package's own: a caller that needs something this
  wrapper does not offer reaches the pool directly.
- **The configuration shape is fixed here.** `dsn` and `max-conns` are the same
  items under every mounting namespace; a setting only one backend has goes on
  that backend's own carrier, not on this one.
- **The `pgtest` fixture lives with this unit.** It is a package of its own in
  this directory: the server it hands out is the one this unit's pool reaches.
  Every test that needs PostgreSQL uses it — the database module and the postgres
  backends of the key-value and event modules — and one shared fixture is what
  keeps them from drifting onto differently behaved servers. This unit and the
  fixture both sit under the `pkg` module's `internal/`, so nothing outside that
  module can import either. The same record covers `pkg/internal/redis/redistest`
  and `pkg/internal/natstest`.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this package alone, inside the `pkg` module:

```
go -C pkg build ./internal/pgpool/...
go -C pkg vet ./internal/pgpool/...
go -C pkg test ./internal/pgpool/...
GOWORK=off go -C pkg build ./internal/pgpool/...   # the standalone leg, without the workspace
```

The package's own cases need nothing beyond the Go toolchain; the scope above
also reaches `pkg/internal/pgpool/pgtest`, whose cases start a container through
the docker CLI.
