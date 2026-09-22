# pkg/internal/natstest

A NATS server for tests to run against: one named container per machine, reused
between runs, with isolation by bucket or stream name rather than by server.

It registers no module and delivers no capability. It is a shared unit, neither
a capability directory nor a program, and it exists because the key-value and
event modules' NATS backends would otherwise each carry their own copy of
"start a server, read its published port, wait for it to answer" — copies that
would differ in nothing, cannot cover each other, and drift onto differently
behaved servers. The cost of that sharing — one shared unit whose changes reach
two modules — is recorded in
[`adr-shared-backend-unit-2026-09-21.md`](../../../docs/adr/adr-shared-backend-unit-2026-09-21.md),
which `pkg/internal/pgpool/pgtest` and `pkg/internal/redis/redistest` share.

## Boundaries

- **No module is registered here and no capability is delivered.**
- **It is reachable only from inside the `pkg` module.** It sits directly under
  that module's `internal/`, so nothing outside that module can import it.
- **The only caller-facing entry point is `URL`.** Everything else is
  unexported on purpose: what a fixture promises is "a server that answers",
  and the rest is its own business.
- **Nothing a case supplies reaches a command line.** Every argument to the
  docker CLI is one of this file's own constants; keep it that way.

## Running it

It is exercised through its callers — `go -C pkg test ./kv/...` and
`go -C pkg test ./event/...` — and needs nothing but the Go toolchain and the
docker CLI. A machine without docker skips those cases unless `CI` is set, in
which case they fail instead: a gate that is silently skipped wherever it is
meant to run is not a gate.
