# pkg/authn/guard

The step-up middleware of the authentication capability: a gin handler a route
puts in front of its own handler so that only an identity strong, fresh or local
enough reaches it. `Require(authn.Requirement)` returns the middleware, and a
request that does not meet the requirement is answered with the 401 that names
`insufficient_user_authentication`.

## It registers no module and imports no factor

This is not a module. It is not in the roster of a command, it delivers no
capability, and its absence from an assembly is not a failure: taking it up is a
call site in a host's or a permission module's route registration. It therefore
stands beside `authn/authntest`, which is the other package of this capability
that no command names.

It imports the root package, the HTTP entry point's failure shape and gin, and
nothing else — no factor module, no store and no other package of this
capability. The decision is a function of the identity alone, so there is nothing
to resolve and no dependency to fail at start.

## Where the authority is

- What it is and the exact answer it writes:
  [`docs/design/modules/design-authn-mfa.md`](../../../docs/design/modules/design-authn-mfa.md),
  the section on the step-up answer and `guard.Require`. The RFC 9470 parameters
  and the schemes the gateway stamps are named there.
- The challenge the rest of the capability writes, and why `Basic` is not among
  them:
  [`docs/design/modules/design-authn-gate.md`](../../../docs/design/modules/design-authn-gate.md).
- The decision it carries out — why a verdict is closed, and where strength is
  composed:
  [`docs/adr/adr-authn-verdict-2026-10-10.md`](../../../docs/adr/adr-authn-verdict-2026-10-10.md),
  [`docs/adr/adr-authn-mfa-2026-10-10.md`](../../../docs/adr/adr-authn-mfa-2026-10-10.md).

This file states only what those do not: the boundaries a change here must not
cross.

## Boundaries

- **The decision is `Identity.Satisfies` and nothing else.** The middleware reads
  the identity the gateway stamped and asks the root package whether it meets the
  requirement. It re-derives no level, reads no factor and consults no store, so
  it cannot disagree with the one place the level is composed. Its whole body past
  that is the shape of the refusal.
- **The failure direction is closed.** A request with no identity is refused, not
  passed: the middleware refuses unless the identity is present and satisfies the
  requirement, so it stays safe if it is ever mounted ahead of the gateway
  (`TestARequestWithNoIdentityIsRefused`, `Require`).
- **The challenge names the identity's own carrier, and no scheme it cannot trust
  reaches the field.** The scheme is `Identity.Challenge()`, the folded name the
  gateway stamped from the credential's carrier. A value that is not an RFC 9110
  token — which a zero identity or a defect could carry — is written as `Bearer`,
  because a 401 with no scheme, or one that put a line ending into the field,
  would not be a challenge at all (`challengeScheme`,
  `TestASchemeThatIsNotATokenIsWrittenAsBearer`).
- **`acr_values` is the level's number and `max_age` is the requirement's
  seconds.** The number is the assurance level's decimal text, the same text a
  token's `acr` claim carries; `max_age` is written only when the requirement asks
  for freshness, in whole seconds rounded up, and a requirement that no identity
  satisfies writes no `max_age`
  (`TestTheChallengeNamesTheLevelTheAgeAndTheScheme`,
  `TestARefusalIsTheSameShapeTheRestOfTheCapabilityWrites`).
- **The refusal is the entry point's, not a body of this package's choosing.** The
  header is set and then `http.Fail` writes the 401 with
  `Problem.Type = urn:speed:authn:insufficient_user_authentication`; a delivered
  `ErrorResponder` decides the body, as it does for every other failure
  (`refuse`).

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

On this package alone:

```
go -C pkg build ./authn/guard/...
go -C pkg vet ./authn/guard/...
go -C pkg test ./authn/guard/...
go -C pkg test -race ./authn/guard/...
env -C pkg golangci-lint run ./authn/guard/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

`guard_test.go` drives the middleware over a gin engine with identities built by
`authntest`, and holds its response equal to the one the rest of the capability
writes. `live_test.go` assembles the real entry point and the real gateway with a
stand-in verifying backend on a loopback port, mounts a guarded route on the
entry point's own endpoint, and holds the full header a client reads.
