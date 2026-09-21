# pkg/redis

The Redis client that the key-value and event modules' redis backends share. It
registers no module and delivers no capability: it wraps "read an address, build
a client, close it" so that the two backends do not each carry a copy of it and
drift apart on what connecting means.

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
- **The client type is the interface.** `Connect` hands back a `*redis.Client`,
  not an abstraction of this package's own: a caller that needs something this
  wrapper does not offer reaches the client directly.
- **The configuration shape is fixed here.** `addr`, `password`, `db`,
  `pool-size`, `dial-timeout` and `read-timeout` are the same items under every
  mounting namespace; a setting only one backend has goes on that backend's own
  carrier, not on this one.
- **`redistest` lives here on purpose.** Both backends' integration tests need a
  server to run against, and one shared fixture is what keeps two backends from
  drifting onto differently behaved servers. It is exported rather than internal
  for that reason, and the same record covers `pkg/pgpool/pgtest` and
  `pkg/natstest`.

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
go -C pkg/redis build ./...
go -C pkg/redis vet ./...
go -C pkg/redis test ./...
GOWORK=off go -C pkg/redis build ./...   # the standalone leg, without the workspace
```

The module itself needs nothing beyond the Go toolchain; `redistest` starts its
container through the docker CLI, the way the other fixtures do.
