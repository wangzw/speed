# pkg/authn

The authentication release unit: the interface package a backend implements and
a handler reads an identity from, `pkg/authn/gate` — the gateway module that
guards listening endpoints — and `pkg/authn/static`, the first credential
backend, whose subject table comes from configuration.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authn.md`](../../docs/design/modules/design-authn.md).
- The decisions behind it, one file per decision:
  [`docs/adr/`](../../docs/adr/) — `adr-authn-scope-2026-09-20`,
  `adr-authn-packaging-2026-09-20`, `adr-authn-wiring-2026-09-20`,
  `adr-authn-ownership-2026-09-20`, `adr-authn-verdict-2026-09-20`,
  `adr-authn-guarantees-2026-09-20`.
- The API itself: the godoc in this package. Read `Verdict`'s constructors and
  `Claim`'s field comments before writing a backend.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **The root package registers no module.** Every backend implements its
  interfaces and every handler that reads an identity imports it, so a module
  registered here would put an authentication layer into the available set of
  every binary that touches either. Registration lives in the subpackages:
  importing `pkg/authn/gate` is what decides that a binary has an
  authentication entry at all, and importing a backend is what decides which
  credentials it can check.
- **A verdict cannot be built literally, and an identity cannot be written.**
  Both types live in `internal/` packages and are re-exported here as aliases.
  That is what makes the zero verdict mean *nobody answered* rather than
  something a backend can assemble by hand, and what keeps the context
  injection point closed to everything outside this release unit — the
  compiler enforces both, and a change that moves either type into this
  package removes the enforcement without removing the claim.
  `authn_test.go` records the half of that property a test can observe and why
  the other half has no case.
- **The readers of a verdict are functions, not methods.** A method would
  travel with the type through the alias and be callable from anywhere. Adding
  one to `internal/verdict.Verdict` is the shape to reject.
- **A subject never carries `Separator`.** The separator is what an owner
  qualification is split on, so a subject holding one could not be told from a
  qualification. It is refused when a claim is built and again when a verdict
  comes back, and both checks are load-bearing.
- **The gateway interprets no credential except to read a subject out of
  `Basic`.** Any other scheme is the claiming backend's to read. A `switch` on
  a scheme name in `gate`, beyond that one, is the shape to reject.
- **A gateway-produced refusal is charged to the gateway, never to a backend.**
  The attribution is in the log and nowhere else, and it decides which of two
  people has something to fix. The four counting buckets are named by
  `verdict.Outcome.String` and are fixed once a dashboard reads them.

## Shape of a backend

A backend is a module that delivers `Verifier` non-exclusively. It reports a
name, reports the subject space it is the authority for, and answers about one
credential at a time. The admission conditions are in the design's extension
points section; the two that are easiest to get wrong are here.

`Prepare` is not optional. A module taken into a command has to start, so a
configuration it cannot run on — a required item nobody gave, a table naming no
subject — is a startup failure whose text names what is absent; there is no
stand-down for a backend to take. Declaring `Prepare` puts that failure ahead of
every construction. A module without one reads its configuration for the first
time in `New`, where the same absent item still stops the startup, one
construction later and after the modules ordered ahead of it have been built.
The descriptor field reads:

```go
return core.Module{
	Name: ModuleName,
	// Exclusive is left empty, which is what lets several backends run
	// at once, each the authority for its own subjects.
	Provides: []core.Provision{{Token: (*authn.Verifier)(nil)}},
	Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
		reader, err := core.Resolve[config.Reader](reg)
		if err != nil {
			// The reader being absent is the host's omission,
			// carried out as it stands. Reading it as "nothing
			// was configured" would lose a whole backend in
			// silence.
			return core.Enablement{}, err
		}
		return prepare(reader)
	},
}
```

A backend that cannot reach its credential source answers `Unavailable`, never
`Denied`. The two are a legal verdict each, so nothing in this package can
catch the mistake — and every lockout, rate limiter and alert rule is keyed off
which one it is:

```go
func (b *backend) Verify(ctx context.Context, subject string, c authn.Credential) authn.Verdict {
	fact, err := b.store.Lookup(ctx, subject)
	if err != nil {
		return authn.Unavailable(err)
	}
	return fact.check(c)
}
```

A backend must also return when the context it was handed runs out. The call
budget is a deadline given to the backend, not a blade the gateway swings: the
call is synchronous, so a backend that ignores the deadline keeps its request
goroutine and one slot of the in-flight gate until it returns. What the gateway
can do on its own side is refuse to honour a late answer — a pass that arrives
after the budget ends the request as an outage — and cap how many calls are
inside a backend at once.

`pkg/authn/static` is the worked example. It never answers `Unavailable`,
because its table is in memory from startup and there is no moment at which it
is present and unable to say.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

This module on its own:

```
go -C pkg/authn build ./...
go -C pkg/authn test ./...
go -C pkg/authn test -race ./...
```

The tests bring up their own gateways and ask for no service, no database and
no environment. `examples/orders-host` is where the assembly is held
from outside the process: it starts a real binary, reaches it over a socket and
checks the status codes, which is the only place the registration of the
middleware layer is observable at all.
