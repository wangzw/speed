# pkg/internal/redis

The Redis client that the key-value and event modules' redis backends share. It
registers no module and delivers no capability: it wraps "read an address, build
a client, close it" so that the two backends do not each carry a copy of it and
drift apart on what connecting means.

## Boundaries

- **No module is registered here and no capability is delivered.** The backends
  do both, each under its own namespace and with its own `Config` mount.
- **The client type is the interface.** `Connect` hands back a `*redis.Client`,
  not an abstraction of this package's own: a caller that needs something this
  wrapper does not offer reaches the client directly.
- **The client `Connect` builds retries nothing.** A command whose reply was
  lost may have run, and sending it again runs it twice — a double increment,
  a winning swap reported as lost, a stream entry appended twice. The library
  retries by default, so `Connect` turns it off, and
  `TestACommandWhoseReplyIsLostIsNotSentAgain` holds that against a server that
  loses replies. Whether to repeat an operation is the caller's decision.
- **The configuration shape is fixed here.** `addr`, `password`, `db`,
  `pool-size`, `dial-timeout` and `read-timeout` are the same items under every
  mounting namespace; a setting only one backend has goes on that backend's own
  carrier, not on this one.
- **The `redistest` fixture lives with this unit.** It is a package of its own in
  this directory: the server it hands out is the one this unit's client reaches.
  Both backends' integration tests need a server to run against, and one shared
  fixture is what keeps two backends from drifting onto differently behaved
  servers. It starts nothing and manages no container — the server is the one
  the repository root's compose file brings up — and a server that does not
  answer fails the case rather than skipping it. This unit and the fixture both
  sit under the `pkg` module's `internal/`, so nothing outside that module can
  import either. The same record covers `pkg/internal/pgpool/pgtest` and
  `pkg/internal/natstest`.

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
go -C pkg build ./internal/redis/...
go -C pkg vet ./internal/redis/...
go -C pkg test ./internal/redis/...
GOWORK=off go -C pkg build ./internal/redis/...   # the standalone leg, without the workspace
```

The package's own cases need nothing beyond the Go toolchain; the scope above
also reaches `pkg/internal/redis/redistest`, whose cases need the external
backends the repository root's compose file declares and carry the `integration`
tag.
