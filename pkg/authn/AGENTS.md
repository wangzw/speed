# pkg/authn

The authentication capability directory: the interface package a backend
implements and a handler reads an identity from, `pkg/authn/gate` — the gateway
module that guards listening endpoints — `pkg/authn/static`, the first
credential backend, whose subject table comes from configuration, and
`pkg/authn/authntest`, which builds the context a gateway would have left on a
request, and the configuration reader a backend's tests need, for the tests of
modules that read an identity. The value types and the
units the modules share live in `pkg/authn/internal/`, which only this directory
can import.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authn.md`](../../docs/design/modules/design-authn.md).
- The decisions behind it, one file per decision, in
  [`docs/adr/`](../../docs/adr/):
  `adr-authn-account-lifecycle-2026-10-10`, `adr-authn-accounts-2026-10-10`,
  `adr-authn-credential-dispatch-2026-10-10`,
  `adr-authn-decision-seam-2026-10-10`,
  `adr-authn-events-and-audit-2026-10-10`, `adr-authn-exemption-2026-10-10`,
  `adr-authn-external-binding-2026-10-10`, `adr-authn-failure-2026-10-10`,
  `adr-authn-guarantees-2026-10-10`, `adr-authn-identity-2026-10-10`,
  `adr-authn-impersonation-2026-10-10`, `adr-authn-login-flow-2026-10-10`,
  `adr-authn-mfa-2026-10-10`, `adr-authn-mfa-recovery-2026-10-10`,
  `adr-authn-oauth-flow-2026-10-10`, `adr-authn-packaging-2026-10-10`,
  `adr-authn-password-hash-2026-10-10`, `adr-authn-scope-2026-10-10`,
  `adr-authn-session-2026-10-10`, `adr-authn-settings-and-secrets-2026-10-10`,
  `adr-authn-sms-2026-10-10`, `adr-authn-sso-2026-10-10`,
  `adr-authn-state-placement-2026-10-10`, `adr-authn-throttling-2026-10-10`,
  `adr-authn-token-2026-10-10`, `adr-authn-verdict-2026-10-10`,
  `adr-authn-wechat-2026-10-10`, `adr-authn-wiring-2026-10-10`,
  `adr-tenant-admission-2026-10-10`
- The API itself: the godoc in this package. Read `Verdict`'s constructors and
  `Accepts`'s field comments before writing a backend.
- What is implemented and what is not: the implementation-status section of the
  design document that owns the capability, with `design-authn-gate` for the
  gateway and the password table. The boundaries and the backend shape below
  describe the code as it stands.

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
  injection point closed. The compiler keeps every package outside this
  capability directory from importing an `internal/` package, and a change that
  moves either type into this package removes that enforcement without removing
  the claim. It says nothing about the packages inside the directory, so the
  writers of an identity are a closed list that `deps_test.go` holds: this
  package, which re-exports the read side and nothing that writes; `gate`, the
  one writer on the request chain, which writes only an identity that has passed
  its exit check; `login`, the login engine, which derives a context with
  `identity.With` for the one audit record of a login and hands it to nothing
  else; and `authntest`, whose builder is for tests. A backend, a login method or
  any other package that imports `internal/identity` in a file that is not a test
  fails that test. `Step`, a login method's answer to the login engine, is the
  third type of that kind and holds to the same rules. `authn_test.go` records
  the half of that property a test can observe and why the other half has no
  case.
- **The readers of a verdict and of a step are functions, not methods.** A
  method would travel with the type through the alias and be callable from
  anywhere. Adding one to `internal/verdict.Verdict` or to `internal/step.Step`
  is the shape to reject; `Step.WithSends` is the one method, because it is how
  a method author adds to an answer. The functions are open to every importer:
  the constructors are public through this package, and a reader that only reads
  cannot make an answer the constructors would not. The readers of an `Identity` are methods
  and each one that returns a slice or a map returns a copy, because an
  identity has no write entry and a caller must not be able to make one.
- **The value types are shapes, never policy.** `internal/model` holds the
  closed sets, the constants, the format checks and the plain records, imports
  only the standard library and no package of this directory, and is imported by
  `identity`, `verdict` and `step`, which import nothing else of ours and never
  each other. What a backend may claim — which account namespace it may issue
  from, how large an attribute set may grow, how far a clock may drift, which
  assurance is believed — is the gateway's judgement and is made in
  `gate/exitcheck.go` from facts only the gateway holds. A check that moves into
  `model` or `identity` would run on every `Identity` built anywhere, test
  fixtures included. `deps_test.go` holds the import lines of the root package
  and of those four.
- **An account lies in the namespace its backend declared.** `Accepts.Accounts`
  is a prefix. The empty one is the library namespace, for the backends that
  answer for accounts the account module keeps; a backend that is not for
  library accounts declares a non-empty prefix of its own; two non-empty
  prefixes never nest; and `static.` belongs to the backend named `static` and
  to no other, which in turn answers for no other prefix. An account and an
  actor outside the returning backend's namespace are refused before an
  identity is stamped, which is what stops a backend from stamping an identity
  that belongs to another one. Every carrier — an `Authorization` scheme or a
  cookie name — has one backend, and two claimants are a startup failure.
- **A pass is a claim until the gateway has checked it.** The principal a
  backend returns is held to the exit check in `gate/exitcheck.go` — the account
  format, the actor's relation to the account with a session and a scope beside
  it and a scope never without an actor, the method set, the assurance, the
  authentication time no more than 120 seconds ahead of the gateway's clock, the
  shapes of the scope and the provider, the attribute bounds, the namespace, and
  that an actor or a scope, a provider or an IdP's assurance were declared in
  `Accepts` — and an unstated assurance is stamped as a single factor there.
  `Backend` and `Challenge` on the identity come from the verifier's name and
  the credential's scheme and from nothing a backend handed over. A refusal of
  this kind is charged to the gateway and written at Error, and its reason never
  carries a value the backend put in the principal. The reserved attribute
  prefixes `idp.` and `authn.` are kept by whoever writes an attribute, not
  checked here, because the gateway cannot tell this capability's own backends
  from a host's.
- **The gateway interprets no credential.** It routes one by the carrier it
  arrived on, the scheme or the cookie name a backend declared, and hands it
  over unread; what a `Basic` pair or a token holds is the claiming backend's to
  read. The one place `gate` names a scheme is the cross-origin check
  (`scriptWrittenScheme` in `gate/serve.go`): a browser keeps a `Basic` pair
  after a challenge and sends it again on a request another site caused, as it
  does a cookie, and handles `Digest`, `Negotiate` and `NTLM` the same way, but
  never attaches a `Bearer` header, which a page's own script writes. The
  gateway reads nothing of what the credential holds and cannot tell whether a
  scheme it was told about is one a browser keeps, so a cookie and every header
  scheme but `Bearer` are held to the check, a scheme nobody listed included,
  and a list of the schemes browsers keep is the shape to reject. Any other
  `switch` on a scheme name in `gate`, or a branch on what a credential holds,
  is the shape to reject. Two claimed cookies on one request,
  of one name or of two, are refused as a whole and no cookie is cleared,
  because which one was meant cannot be known. It answers who the caller is and nothing about
  what the caller may do, so it writes 401 and 503 and never 403: a missing
  credential, a refused one and an identity outside the routes its kind may
  reach are all 401. A route handler of this capability directory may write 403
  for a policy refusal it relays, and the gateway may not.
- **A masking rule governs what is written after it is registered.** The gateway
  registers the credential key names and the value shapes in `New`, and the rule
  over the word that follows a scheme in `Init`, because the schemes are the
  backends' declarations and are read there. It interprets no scheme, so `Basic`
  is masked only when a backend claims it. An attribute bound to a logger with
  `With` is judged once, when it is bound, and a rule registered later does not
  reach it: no module binds a sensitive attribute to a logger in its own `New`.
  A module that holds a secret configuration value registers the value itself as
  a literal matcher in its own `New`, before it takes its `Named` logger, through
  `internal/literal`; marking an item `Sensitive` does not do that. The matcher
  finds the value as written, without the whitespace around it and URL-escaped,
  because a failed request's text carries the address it was sent to. A module
  that builds a matcher of its own for a configured secret is the shape to reject.
- **A secret appears in no log line, no error text, no event, no audit entry
  and no metric attribute.** A password, a token, a cookie value, a challenge
  answer and a configured secret are written nowhere a reader of those outputs
  can reach, and a refusal's reason names the rule that failed and never the
  value that failed it. A test that drives a secret through a path asserts its
  absence from the log, the error and the response; a path with no such test is
  not known to be clean.
- **No package of this directory imports `pkg/tenant`.** The direction is fixed:
  tenancy depends on authentication, and the admission layer that joins the two
  lives in the tenant directory. `deps_test.go` holds every package under this
  directory to that, in every non-test file whatever its build constraint, and
  through the packages outside the directory that it imports.
  `gate/deps_test.go` holds the gateway's closure to the same and, beyond it, to
  no store, no key-value primitive, no event bus and no settings module.
- **The capability interfaces one module delivers have pairwise disjoint method
  names.** A module has one instance and `core.Resolve` asserts it against each
  delivered interface, so two interfaces whose methods share a name with
  different signatures cannot both be satisfied by the one type. A new
  capability interface is checked against the module's other interfaces for a
  clash before it is added, and each module carries a compile-time
  `var _ authn.X = (*product)(nil)` assertion for everything it delivers.
- **Which endpoint the routes go on and which origins the cross-origin check
  trusts are each decided in one place.** `internal/mount` holds `Endpoint` and
  `TrustedOrigins` with `CrossOrigin`. The gateway is the only caller today; the
  module that mounts routes does not exist yet, and when it does it calls the
  same functions. The gateway cannot hand its check to the Mounter, because a
  host may supply a `Gate` of its own, so each builds an instance from the same
  rule. Every trusted origin is held in the form a browser sends it, and an entry
  that cannot be turned into one is a startup failure, never skipped. A module
  that derives the trusted set again, or picks a default endpoint by a rule of
  its own, is the shape to reject. The gateway judges the
  endpoint the routes go on — known, guarded, outside the admission layer and
  outside `tenant.resolve.endpoints` — when the key names one, and when it is
  empty only if a module that mounts routes delivers a `Mounter`.
- **A request to a URL an outside party chose goes through `internal/egress`.**
  The client it builds judges every connection attempt on the address actually
  connected to, speaks https only, follows no redirect, caps a response body,
  uses no proxy and never switches certificate verification off. A caller that
  builds an `http.Client` of its own for such a URL, or relaxes one of those
  properties, is the shape to reject. Its address table is checked against the
  IANA special-purpose registries, and what each block was checked for is in the
  tests.
- **A password entry is read and bounded before anything is derived from it.**
  `internal/password` is where an entry is parsed, derived against, cached and
  backed off. The bounds an entry is held to — memory, iterations, lanes, salt
  and key lengths — are constants of that package and not input items, so that
  what an entry says cannot decide what one check costs. Derivations run under a
  `Governor`, which caps how many run at once and how much memory they hold, and
  a derivation that finds no room within the wait its caller allows is refused as
  busy and not queued. A caller that derives from a string it did not parse, or
  makes a bound configurable, is the shape to reject. The back-off for a
  credential presented on every request counts an attempt when it admits it
  (`Throttle.Reserve`), and a pass gives back that attempt and nothing else: a
  success that clears the earlier failures, or a check, a derivation and a
  record of the failure as separate steps that are not one atomic one, is the
  shape to reject. A password that is remembered from a derivation that held is
  not a guess and reserves nothing (`Throttle.Standing` is its read-only check
  of the failures that stand): a cache hit that takes an attempt makes the
  requests of one name refuse one another under parallel load. The table of keys
  is bounded, and the keys of the names a backend answers for are held outside
  the bound.
- **A time written to a database or bound as a query parameter is read through
  `internal/clock`, in UTC.** SQLite compares times as text in the zone they
  carry. A time that is only compared in memory, such as a cache's lifetime or a
  back-off window, may come from the process clock. `internal/acctcache` is a way of reading less,
  never a way of deciding: an entry stops being served when its lifetime ends,
  and a lifetime of zero turns the cache off.
- **A gateway-produced refusal is charged to the gateway, not to a backend,
  with one exception.** The attribution is in the log and nowhere else, and it
  decides which of two people has something to fix, so it follows which side the
  cause came from. A refusal of a pass the gateway judged itself, at the exit
  check or on the impersonation scope, carries the rejected challenge of the
  backend whose credential it was, because the request did carry a credential
  that backend claims. The exception is an impersonating pass that reached a route
  neither registered for impersonation nor on an endpoint the scope guard
  covers: the gateway makes the judgement, and it is charged to the backend
  whose pass it declines, at Info, with the cause `impersonation-scope`. The counting buckets are `authenticated`,
  `denied`, `unavailable`, `unspoken` and `anonymous`, the first four named by
  `verdict.Outcome.String`, and they reach a dashboard through the metric
  capability as the `authn.gate.conclusions` counter on the meter `authn`, one
  `outcome` attribute value per bucket; the bucket names, the counter's name,
  the meter's name and the attribute's name are all fixed once a dashboard reads
  them.
- **The static backend's entry format is a contract with whoever writes the
  table.** `pkg/authn/internal/password/phc.go` states it, and
  `pkg/authn/static/hash.go` writes it. A table sits in a configuration file and
  outlives the binary that reads it, and tools other than this package write
  entries to it, so changing the PHC shapes an entry may take, the base64
  variant or the key length an algorithm derives breaks every table already
  written. The legacy `pbkdf2-sha256$` form is read and never written. Another
  derivation goes in beside the two under an algorithm name of its own.
  `TestTheStoredFormatIsTheOneTheDesignStates` holds the format in literals, and
  a change that has to edit those literals is the shape to reject.
- **`authntest` is for tests.** Its exported surface is `Context` and `Anonymous`,
  which build the context a gateway would have left, with the options that name
  the fields of the identity and the access level (`WithChallenge` among them),
  and `ConfigReader` and `ConfigReaderAll`, which build the configuration reader
  a backend is tested against; `surface_test.go` pins the list. `Context` takes a
  `testing.TB` and restates the shape and the bounds the gateway's exit check
  holds an identity to, so an identity built with it is of a kind the gateway
  could stamp. It does not restate the whole check: the 120-second offset of the
  authentication time, the account namespaces other than `static.`, the
  capabilities a backend declares in `Accepts` and the reserved attribute prefixes
  need the gateway's clock or the backends it holds and are not checked. The
  shapes and bounds it does restate are a second copy of the gateway's, and a
  change to one is a change to the other;
  `TestAuthntestAndTheExitCheckAgreeOnEveryShapeAndBound` in
  `gate/exitcheck_parity_test.go` is the only thing that holds the two
  together. A `testing.TB` is no barrier to
  production code, since a value that nobody runs is a valid one. The only
  defence is the dependency test in this directory, which fails `go test` for any
  package of the `pkg` module that imports it in a file that is not a test. That
  guard covers the `pkg` module only: the example hosts are separate modules in
  `go.work`, so every module there that imports `authn` carries its own copy of
  the check (`examples/orders-host/authntest_test.go` and
  `examples/crm-host/authntest_test.go`), which reads every non-test file with
  `go/parser` whatever its build constraints say, because `go list` sees only
  the files that build for the platform and tags of the run. A new host module
  added without one is not covered.

## Shape of a backend

A backend is a module that delivers `Verifier` non-exclusively. It reports a
name that is a constant of its code, declares in `Accepts` which carriers it
takes whole and what kind of identity it may hand back, and answers about one
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
	// at once, each the authority for its own carriers.
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

A backend says what it takes and what it may hand back:

```go
func (b *backend) Accepts() authn.Accepts {
	return authn.Accepts{Schemes: []string{"basic"}, Accounts: "corp."}
}
```

A pass carries a `Principal`: the account, the methods the credential was
verified by, the assurance and the time of the authentication. The gateway holds
the account to the namespace the backend declared:

```go
return authn.Authenticated(authn.Principal{
	Account:   authn.AccountID("corp." + subject),
	Methods:   []authn.Method{authn.MethodPassword},
	Assurance: authn.AssuranceSingle,
	AuthTime:  time.Now(),
})
```

A backend that cannot reach its credential source answers `Unavailable`, never
`Denied`. The two are a legal verdict each, so nothing in this package can
catch the mistake — and every lockout, rate limiter and alert rule is keyed off
which one it is:

```go
func (b *backend) Verify(ctx context.Context, c authn.Credential) authn.Verdict {
	fact, err := b.store.Lookup(ctx, c.Value)
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
so there is no moment at which it is present and unable to say, and it answers
`Unavailable` only for a context that ended before a derivation starts, and
when no capacity is free because the width or the memory budget is taken; it
does not wait for either. An attempt that needs a derivation is reserved when it
is checked and settled when it is known: a pass gives back its own reservation and leaves the failures
before it to age, a failure keeps its reservation, and an `Unavailable` answer
gives it back, because a context that ended before a derivation began, or a
width that was taken, is not a guess. The derivation takes no context, so a
wrong password whose caller left in the middle of it is still counted: leaving
would otherwise be a way to try passwords without the back-off seeing them.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

This directory is a package tree of the `pkg` module, which owns the only
`go.mod`, so it is built and tested through that module:

```
go -C pkg build ./authn/...
go -C pkg test ./authn/...
go -C pkg test -race ./authn/...
env -C pkg golangci-lint run ./authn/...
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
starts a real binary, reaches it over a socket and checks the status codes, the
`Basic` challenge and the back-off of a name that keeps presenting a wrong
password.
