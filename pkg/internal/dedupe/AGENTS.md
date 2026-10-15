# pkg/internal/dedupe

The bounded, in-process outcome table that the sending capabilities' carriers
share. It registers no module, delivers no capability and takes part in no
lifecycle stage: it wraps "remember the outcome of what a carrier has already
been asked to do, keyed by a stable identifier", so that the holders do not each
carry a copy of the eviction and the settle-then-answer bookkeeping and drift
apart on what "already asked" means.

## Boundaries

- **No module is registered here and no capability is delivered.** The carriers
  do both, each in its own `New`, under its own namespace.
- **It sits here, outside a capability directory, because two capability
  directories use it.** The carriers under `pkg/sms` and the carriers under
  `pkg/mail` both hold a `Table`; a package under `pkg/sms` cannot import an
  `internal` package of `pkg/mail`, and both directories are under the one `pkg`
  module's `internal/`. A table a carrier builds for itself, instead of calling
  this unit, is the shape to reject.
- **The memory is one process, which is the adapter's own.** Across replicas each
  keeps its own, so holding a vendor to a single call across replicas is the
  caller's, not this unit's.
- **Eviction stops at the first operation still in flight.** Dropping an
  unsettled entry would let a later call for the same identifier run again while
  the first was still going, which is the case the table exists to prevent.
- **A carrier is not idempotent, so the table answers a repeat with the earlier
  outcome instead of a second call.** A second send would deliver and bill a
  second message; the table exists so that one identifier reaches the carrier at
  most once.

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
go -C pkg build ./internal/dedupe/...
go -C pkg vet ./internal/dedupe/...
go -C pkg test ./internal/dedupe/...
```

The package's own cases need nothing beyond the Go toolchain.
