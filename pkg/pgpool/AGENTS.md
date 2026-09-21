# pkg/pgpool

The PostgreSQL connection pool that the key-value and event modules' postgres
backends share. It registers no module and delivers no capability: it wraps the
pool construction — parsing the connection string, carrying the defaults, and
refusing a size the pool cannot take — so that the two backends do not each
carry a copy of it.

## Where the authority is

- Why a release unit that registers nothing exists at all, and what sharing one
  costs:
  [`adr-shared-backend-unit-2026-09-21.md`](../../docs/adr/adr-shared-backend-unit-2026-09-21.md).
- What each backend is and why it is shaped that way:
  [`docs/design/modules/design-kv.md`](../../docs/design/modules/design-kv.md)
  and
  [`docs/design/modules/design-event.md`](../../docs/design/modules/design-event.md).

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
- **`pgtest` lives here on purpose.** Both backends' integration tests need a
  server to run against, and one shared fixture is what keeps two backends from
  drifting onto differently behaved servers. It is exported rather than
  internal for the same reason `pkg/redis/redistest` is, and the same record
  covers both.

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
go -C pkg/pgpool build ./...
go -C pkg/pgpool vet ./...
go -C pkg/pgpool test ./...
GOWORK=off go -C pkg/pgpool build ./...   # the standalone leg, without the workspace
```

The module itself needs nothing beyond the Go toolchain; `pgtest` starts its
container through the docker CLI, the way `pkg/redis/redistest` and
`pkg/natstest` do.
