# pkg/internal/cutproxy

A TCP proxy a test can cut: it carries the connections it accepts to one
upstream server, and closes every one of them and refuses new ones until
restored — what an operator's cut between a client and its server looks like
from the client's side.

It registers no module and delivers no capability. It is a shared unit, neither
a capability directory nor a program, and it exists because the key-value and
event modules' NATS backends both test what a call issued while the link is down
may and may not do, and would otherwise each carry their own copy of the same
proxy. Its placement follows
[`adr-shared-backend-unit-2026-09-21.md`](../../../docs/adr/adr-shared-backend-unit-2026-09-21.md):
a fixture with no wrapper package of its own is a unit of its own under the
`pkg` module's `internal/`.

## Boundaries

- **No module is registered here and no capability is delivered.**
- **It is reachable only from inside the `pkg` module**, and only from tests:
  nothing but a `_test.go` file imports it.
- **It carries bytes and cuts links; it speaks no protocol of its own.** A case
  points a client at `Addr` and decides what the bytes are.
- **`Await` is the wait such a case needs, and is not a general-purpose helper
  for other states.** A client library answers whether it is reconnecting or
  connected rather than announcing either, so a case asks until the answer
  changes.

## Running it

It is exercised by its own case and through its callers, which
`grep -rl --include='*_test.go' cutproxy pkg` lists — the callers by their
import of the unit, its own case by its package clause:

```
go -C pkg test ./internal/cutproxy/ ./event/nats/... ./kv/nats/...
```

It needs nothing but the Go toolchain; the callers' cases bring the NATS server
they talk to through `pkg/internal/natstest`.
