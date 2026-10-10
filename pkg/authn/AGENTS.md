# pkg/authn

The authentication capability directory: the interface package a backend
implements and a handler reads an identity from, `pkg/authn/gate` — the gateway
module that guards listening endpoints — and `pkg/authn/static`, the first
credential backend, whose subject table comes from configuration.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authn.md`](../../docs/design/modules/design-authn.md).
- The decisions behind it, one file per decision:
  [`docs/adr/`](../../docs/adr/) — `adr-authn-scope-2026-10-10`,
  `adr-authn-packaging-2026-10-10`, `adr-authn-wiring-2026-10-10`,
  `adr-authn-credential-dispatch-2026-10-10`, `adr-authn-verdict-2026-10-10`,
  `adr-authn-guarantees-2026-10-10`, `adr-authn-exemption-2026-10-10`,
  `adr-authn-password-hash-2026-10-10`, `adr-authn-identity-2026-10-10`,
  `adr-authn-accounts-2026-10-10`. The list is an excerpt: the design's
  per-topic documents link the rest.
- The API itself: the godoc in this package. Read `Verdict`'s constructors and
  `Claim`'s field comments before writing a backend.
- What is implemented and what is not: the implementation-status section of the
  design. The boundaries and the backend shape below describe the code as it
  stands; where the status section lists the credential seam, the identity
  fields or the password table entry as partly implemented, the design's shape
  is the one a change moves toward, and these sentences change together with
  that code.

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
  injection point closed to everything outside this capability directory — the
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
  `verdict.Outcome.String` and reach a dashboard through the metric capability
  as the `authn.gate.conclusions` counter, one `outcome` attribute value per
  bucket; the bucket names, the counter's name and the attribute's name are
  all fixed once a dashboard reads them.
- **The static backend's entry format is a contract with whoever writes the
  table.** `pkg/authn/static/hash.go` states it. A table sits in a
  configuration file and outlives the binary that reads it, and tools other
  than this package write entries to it, so renaming the scheme, changing the
  separator or the base64 variant, or deriving another key length breaks every
  table already written. Another derivation goes in beside it under a scheme
  name of its own. `TestTheStoredFormatIsTheOneTheDesignStates` holds the
  format in literals, and a change that has to edit those literals is the shape
  to reject.

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
can do on its own side is refuse to honour a late answer — a pass or a refusal
that arrives after the budget ends the request as an outage — and cap how many
calls are inside a backend at once.

`pkg/authn/static` is the worked example. Its table is in memory from startup,
so while its context holds there is no moment at which it is present and unable
to say, and it answers `Unavailable` only for a context that ended before the
password derivation began. The derivation is the standard library's, which
takes no context, so that check before it is the one point at which this
backend can honour its context.

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
no environment beyond loopback ports. `gate/assembly_test.go` drives the
gateway's own descriptor through a real registry, which is where what each
callback takes up and registers is observed. `gate/live_test.go` drives the
descriptor together with the real entry point, `pkg/http`, on loopback ports and
sends it real requests: which route a request is matched to, what an exemption
covers, which layer answers a request that matches no route, and the shape of a
refusal are all decided by the entry point, so a case about any of them stays on
that harness — a router written for the test answers what the test decided.
`examples/orders-host` holds the whole assembly from outside the process: it
starts a real binary, reaches it over a socket and checks the status codes.
