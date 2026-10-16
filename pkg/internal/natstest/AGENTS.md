# pkg/internal/natstest

A NATS server for tests to run against: the one the repository's compose stack
brings up, with JetStream on, at a fixed loopback address. Isolation is by
bucket or stream name rather than by server.

It registers no module and delivers no capability. It is a shared unit, neither
a capability directory nor a program, and it exists because the key-value and
event modules' NATS backends would otherwise each carry their own copy of
"where is the server and is it answering" — copies that would differ in
nothing, cannot cover each other, and drift onto differently behaved servers.
The cost of that sharing is one unit whose changes reach two modules;
`pkg/internal/pgpool/pgtest` and `pkg/internal/redis/redistest` share the same
arrangement.

## Boundaries

- **No module is registered here and no capability is delivered.**
- **It is reachable only from inside the `pkg` module.** It sits directly under
  that module's `internal/`, so nothing outside that module can import it.
- **The only caller-facing entry point is `URL`.** Everything else is
  unexported on purpose: what a fixture promises is "a server that answers",
  and the rest is its own business.
- **It starts nothing and manages no container.** The server's version and its
  flags are the compose file's business; this unit reports where to reach it
  and nothing more.
- **A server that does not answer fails the case, and never skips it.** An
  integration run that quietly dropped the NATS leg would be running less of a
  case than it says, and a gate that skips wherever it is meant to run is not a
  gate. The failure names the entry point that brings the stack up.

## Running it

It is exercised through its callers — `go -C pkg test -tags=integration ./kv/...`
and `go -C pkg test -tags=integration ./event/...` — and needs the external
backends, which the repository root's compose file declares:

```
docker compose up -d --wait
```

Without the `integration` tag those cases are not compiled, so a unit run needs
nothing installed.
