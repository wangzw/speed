# pkg/internal/literal

The log-redaction literal matcher register that the authentication login leaves
and the SMS carriers share. It registers no module and delivers no capability: it
wraps "register this configured secret so the logging layer masks it", so that
the holders do not each carry a copy of the construction and drift apart on what
"the value as written" means.

## Boundaries

- **No module is registered here and no capability is delivered.** The holders do
  both, each in its own `New`, under its own namespace.
- **The register depends on `pkg/log`.** The values it builds are the logging
  layer's redaction matchers, so it cannot live in a standard-library-only
  package.
- **It sits here, outside a capability directory, because two capability
  directories use it.** The login leaves under `pkg/authn/login` and the carriers
  under `pkg/sms` both call `Register`; a package under `pkg/sms` cannot import an
  `internal` package of `pkg/authn`, and both directories are under the one `pkg`
  module's `internal/`. A rule a holder builds for itself, instead of calling
  this unit, is the shape to reject.
- **It is not a shape rule.** A client secret or a vendor key has no shape, so the
  rule is an exact, case-sensitive substring over the forms a log line may carry
  the value in: as written without the surrounding whitespace, and URL-escaped as
  a query component or a path segment. A value that is empty or all whitespace has
  no form and is not registered.
- **A rule governs what is written after it is registered, so a holder registers
  in its own `New`, before it takes its `Named` logger.** An attribute bound to a
  logger is judged once, when it is bound, and a rule registered later never
  reaches it. Marking a config item `Sensitive` does not register anything here:
  it keeps the value out of error texts and help output and nothing more.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

CI runs the segments of `make check` as parallel jobs.
On this package alone, inside the `pkg` module:

```
go -C pkg build ./internal/literal/...
go -C pkg vet ./internal/literal/...
go -C pkg test ./internal/literal/...
GOWORK=off go -C pkg build ./internal/literal/...   # the standalone leg, without the workspace
```

The package's own cases need nothing beyond the Go toolchain.
