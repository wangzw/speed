# pkg/authn

The authentication capability directory: the interface package a backend
implements and a handler reads an identity from, `pkg/authn/gate` — the gateway
module that guards listening endpoints — `pkg/authn/static`, the first
credential backend, whose subject table comes from configuration,
`pkg/authn/mount`, which mounts a module's routes under the base path with the
access level the gateway applies to each, the modules of the identity system —
`pkg/authn/account`, `pkg/authn/session`, `pkg/authn/mfa`, `pkg/authn/token`,
`pkg/authn/sso`, `pkg/authn/impersonation`, `pkg/authn/admin` and the login
methods under `pkg/authn/login` — `pkg/authn/guard`, which writes the step-up
challenge of a route that requires a stronger authentication, and
`pkg/authn/authntest`, which builds the context a gateway would have left on a
request, and the configuration reader a backend's tests need, for the tests of
modules that read an identity.
The value types and the units the modules share live in `pkg/authn/internal/`,
which only this directory can import; among them `internal/mount/mounttest` is
a `Mounter` for the tests of the modules that mount routes. The literal-redaction
register is the one shared unit outside it: it is `pkg/internal/literal`, used by
the four login leaves here and by the two SMS carriers under `pkg/sms`, because a
package under `pkg/sms` cannot import an `internal` package of this directory.

The API is the package's godoc. Read `Verdict`'s constructors and `Accepts`'s
field comments before writing a backend.

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
- **The sub-bounds of an attribute set are declared once, in
  `internal/model/bounds.go`.** An identity's attributes come from two writers:
  the account module, which stores the host-writable set, and the login engine,
  which puts the `idp.*` entries of one login into the session payload. Each is
  held to bounds of its own, and the two together to less than the totals the
  gateway stamps; the account module holds its set to `CheckAccountAttributes`,
  the session module holds a session payload to `CheckIDPAttributes` and cuts the
  client fields with `TruncateUTF8`, and the login engine writes its `idp.*`
  snapshot to the model's own numbers (`SessionIDPAttributesMax`,
  `SessionIDPKeyMax`, `SessionIDPValueMax`), cutting each value at a character
  boundary to the value bound; none of the three writes a number of its own. A
  set the checks refuse is `ErrInvalidArgument`. The totals are the gateway's,
  kept in `gate/exitcheck.go`, and `gate/attribute_bounds_test.go` holds them
  equal to the model's copy and puts the largest set of each writer, merged, to
  the exit check.
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
  `pkg/internal/literal`; marking an item `Sensitive` does not do that. The matcher
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
  `mount/deps_test.go` holds the mount module's closure to no gateway, no
  credential backend, no store, no key-value primitive, no event bus, no settings
  module and no tenant: it reaches the gateway through the interface alone, so
  that a host's own gateway takes it in and importing its package registers no
  gateway with every binary that imports this one.
- **`pkg/authn/token` is the one package of this directory that speaks to the
  JOSE library, and the one that delivers `Tokens`.** Every signing,
  verification and JWK-publication call reaches the library through
  `token/jose.go`, so a change of library reaches one file; a `jose` import
  outside `token` is the shape to reject, and `deps_test.go` holds the single
  importer. The module registers as a `Verifier` for the `bearer` scheme, so the
  access tokens it issues are the credentials it checks.
- **The capability interfaces one module delivers have pairwise disjoint method
  names.** A module has one instance and `core.Resolve` asserts it against each
  delivered interface, so two interfaces whose methods share a name with
  different signatures cannot both be satisfied by the one type. A new
  capability interface is checked against the module's other interfaces for a
  clash before it is added, and each module carries a compile-time
  `var _ authn.X = (*product)(nil)` assertion for everything it delivers.
- **A setting of `authn.*` or `tenant.admission.*` is never writable through a
  surface a tenant or an ordinary administrator can reach.** A holder of the
  global `settings.write` permission could relax a back-off, a budget, a prefix
  list, a session period or a relaxation switch at runtime. A global write
  through the settings admin surface is decided in the platform domain and lands
  a mandatory audit row; a host's own write path (a seed command, for one) is
  authenticated by the host and neither happens there. `authn` adds the
  retrospective `Warn` and `authn.settings.relaxed` row for a move toward a
  relaxed value, never a block.
  What the surface lacks is per-key write control, so the requirement is on the
  host's own management surface, and `authn` does not check that a host honours
  it. The relaxation switches are settings: `signup` and `auto-link` declared by
  the account module, `check-session` by the token module. The developer
  switches `mount.allow-insecure` and `sso.allow-private-egress`, and the
  guard-list keys `endpoints` and `trusted-origins`, are configuration read from
  the primary source alone, because they are decided at construction.
- **Which endpoint the routes go on and which origins the cross-origin check
  trusts are each decided in one place.** `internal/mount` holds `Endpoint` and
  `TrustedOrigins` with `CrossOrigin`. The gateway and the module that mounts
  routes both call the same functions: `pkg/authn/mount` reads the same keys and
  builds its registrations from them. The gateway cannot hand its check to the
  Mounter, because a host may supply a `Gate` of its own, so each builds an
  instance from the same rule. Every trusted origin is held in the form a browser
  sends it, and an entry that cannot be turned into one is a startup failure,
  never skipped. A module that derives the trusted set again, or picks a default
  endpoint by a rule of its own, is the shape to reject. The gateway judges the
  endpoint the routes go on — known, guarded, outside the admission layer and
  outside `tenant.resolve.endpoints` — when the key names one, and when it is
  empty only if a module that mounts routes delivers a `Mounter`.
- **What every handler mounted through a `Mounter` shares is in
  `internal/mount`, written once.** The closed set of problem codes, each with
  the one status it is answered in, and the writers that answer in them: `Fail`
  for the 400s, the 409s and the 415, `Unauthorized` and `StepUpRequired` for the
  401s, `Forbidden`, `Throttled`, `Unavailable` and `Busy`. The request body
  decoder `Decode`, which takes only `application/json` and holds a body to 64
  KiB, a body over it being a 400 `bad-request` like every other body that is
  not one object. The wrapper `Wrap`, which every registration passes through: it
  puts `Cache-Control: no-store` on the response from the start and holds an
  unsafe method that is not a callback to the cross-origin check before the
  handler is called. `AllowCallback`, which puts the bypass for a redirect back
  on the `Mounter`'s own check and never on the gateway's. A handler that writes
  a problem body, reads a JSON body or checks the origin in a way of its own is
  the shape to reject. A 401 that a handler or `Wrap` writes offers `Bearer` and
  `Cookie` and never `Basic`, so that a page's `fetch` cannot make the browser
  open its own password prompt, and its realm is the endpoint's name,
  `Mounter.Endpoint()`, which is the realm the gateway writes on that endpoint.
  `mounttest` is the `Mounter` for the tests of those modules: it puts every
  registration through the real `Wrap`, records the kind of each, and gives a
  request the identity a test chose.
- **A module mounts through `mount.Declared`, so the route's level is its
  operation's declaration.** `Declared` reads the descriptor and registers the
  route as public, optional or guarded according to `Operation.Access`; the
  fragment publishes the same field as the operation's `security` requirement.
  Calling `Public`, `Optional` or `Guarded` directly is for the tests of the
  mounting machinery, not for a module's routes: a module that did would be the
  one place where the contract and the gateway could disagree.
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
- **The static backend reports a setting moved toward lenient, after the fact.**
  `pkg/authn/static/relax.go` subscribes to `settings.changed` only when both
  the event bus and the settings store are present, and a change of
  `free-attempts` upward, `fail-window` downward or `max-backoff` downward
  writes one `Warn` carrying the setting key and the direction and one
  best-effort `authn.settings.relaxed` audit row, never a value; with either
  absent the subscription is silent. The same shape is in every module that
  holds such a setting. `TestARelaxingChangeWritesAWarnAndAnAuditRow` holds it.
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
  totals of an identity's attribute set and the longest key and value an entry
  may have are not copied: it reads them from `internal/model/bounds.go`, and
  `gate/attribute_bounds_test.go` holds the exit check's own constants equal to
  them. The other shapes and bounds it restates, the method count, the account
  format, the backend name and the provider's UUID text, are a second copy of the
  gateway's, and a change to one is a change to the other;
  `TestAuthntestAndTheExitCheckAgreeOnEveryShapeAndBound` in
  `gate/exitcheck_parity_test.go` is the only thing that holds those two
  together. A `testing.TB` is no barrier to
  production code, since a value that nobody runs is a valid one. The only
  defence is the dependency test in this directory, which fails `go test` for any
  package of the `pkg` module that imports it in a file that is not a test, with
  one exception: `internal/mount/mounttest`, which builds the identity of each
  request it serves with `authntest`, is the only package besides `authntest`
  that imports it outside a test file. The same test holds `mounttest` to the
  rule: a package that imports it outside a test file registers routes that no
  gateway guards. That
  guard covers the `pkg` module only: a host is a separate module in `go.work`,
  so the check does not reach it. A host that wants the same defence carries its
  own copy — one that reads every non-test file with `go/parser` whatever its
  build constraints say, because `go list` sees only the files that build for
  the platform and tags of the run. A host module added without one is not
  covered.

## The seams

The root package declares the capability interfaces a module, a host or a vendor
leaf implements — the seams — and registers no module of its own, which is the
first boundary above. A seam's method set and the value types it names are a
stable contract: a method added, dropped or re-signed, or a field of one of those
types renamed, is a breaking change for every implementer and every consumer.
`seams_test.go` holds each method set it lists to the declared signature and
`surface_test.go` pins the exported fields and methods. The closed sets a seam
reads — the SMS purposes and the sentinel errors — are declared in this package
beside it. The message languages are not one of them: the set of languages a run
supports is the set the declared catalogs and carrier templates carry, and the
default is the `i18n.default-locale` configuration item. How a module delivers a
seam and how a consumer takes it are stated in the module's own `AGENTS.md`, not
here.

## Shape of a backend

A backend is a module that delivers `Verifier` non-exclusively. It reports a
name that is a constant of its code, declares in `Accepts` which carriers it
takes whole and what kind of identity it may hand back, and answers about one
credential at a time. The two admission conditions that are easiest to get
wrong are singled out below.

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
`starters/demo` holds the whole assembly from outside the process: it starts a
real binary, reaches it over a socket and checks the statuses of the routes the
gateway guards.
