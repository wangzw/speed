# pkg/authz/guard

The authorization enforcement module. Its first face, built here, is the
decider: it implements the authentication capability's decision seam
`authn.Decider`, turning a `Decision` into one question put to the policy
engine, so a module that asks whether an actor may do a thing is answered from
this module's policy. Its other faces — a middleware on the authorization
stage, the route-declaration surface `authz.Gate`, the handler-internal
`Guard` API and the 403/503 write-out — are the same module's and land
here beside the decider; this package builds the decider alone, and the module
declares `authz.guard` and delivers `(*authn.Decider)(nil)` exclusively.

The translation from a permission to the (object, action) pair the engine
matches on lives here and nowhere else, because the decider and, when it
lands, the middleware both read it: a permission cannot mean one thing on one
path and another on the other.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authz.md`](../../../docs/design/modules/design-authz.md) —
  the translation table under "动作到查询的翻译", the decider under "Decider
  的动作分派", the platform fallback under "平台域与跨租户回退", and the module
  row under "登记单元与名字".
- The decisions behind it, one file per decision, in
  [`docs/adr/`](../../../docs/adr/):
  [`adr-authz-decision-seam-2026-10-12`](../../../docs/adr/adr-authz-decision-seam-2026-10-12.md)
  (the seam it implements, the one platform fallback, the fail-closed unknown
  action),
  [`adr-authz-enforcement-2026-10-12`](../../../docs/adr/adr-authz-enforcement-2026-10-12.md)
  (the middleware the decider shares its table with),
  [`adr-authz-packaging-2026-10-12`](../../../docs/adr/adr-authz-packaging-2026-10-12.md)
  (why the decider and the middleware are one module).
- The 403/503 boundary and the refusal detail live in the design document's
  "错误与失败语义"; the decider states the engine's error and leaves the
  response to the middleware and the caller.
- The API itself: the godoc in this package, and the seam in
  `pkg/authn` (`Decision`, `Action`, `Decider`).

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **`Decide` is a closed translation, and an unknown action is refused.** The
  mapping from a permission to the (object, action) pair is the whole set the
  module can express. An action with no row returns `(false, nil)` — a denial
  and not an error — so an action the authentication side added is refused
  until the table is updated, which is the safe direction; the parity guard in
  the parent directory (`action_parity_test.go`) holds the table and the two
  vocabularies aligned. `ObjectAction` is the one reader of the table, shared
  with the middleware when it lands.
- **The subject is the actor's accountability key.** `Decide` puts
  `Decision.Actor.Key()` to the engine, which is always the account whose hands
  are on the request: under an impersonation the decision is made for the
  actor and not for the account the request is taken to be from.
  `TestDecideTakesTheActorsKey`.
- **`authn.tenant.access` is the one action with a platform-domain fallback.**
  It decides in the tenant's own domain first, and only on a miss does it
  decide again in `PlatformDomain`; two misses are a denial. Every other action
  decides in one domain — a tenant-level action in the tenant domain, an
  account-level action in the platform domain when `Decision.Tenant` is empty.
  A decision already in the platform domain runs no fallback.
  `TestTenantAccessDecidesFourWays`, `TestANonAccessActionDecidesInOneQuery`.
- **An engine error is carried out and is neither a denial nor a permission.**
  The decider returns the engine's error as it stands so the caller can read it
  as a 503; turning it into a denial would make a recoverable outage look like
  a permanent permission conclusion.
  `TestDecideCarriesAnEngineError`, `TestDecideCarriesADenial`.
- **The module requires the engine and the catalog, and delivers the seam
  exclusively.** Both are delivered exclusively by `authz.policy`, so a command
  that takes this module in names `authz.policy` alongside it; the engine
  requirement is what makes a decider without an engine fail the startup rather
  than answer 503 on every question. The logger and the meter provider are
  optional: the decision reads neither.
  `TestTheRegistryDeliversTheDecider`, `TestTheRegistryRefusesWithoutACatalog`,
  `TestASecondDeciderFailsTheStartup`.
- **`Prepare` states `StateEnabled` and never `StateAuto`.** `StateAuto` is the
  stance a resolution disables when a second provider claims the seam; this
  module must fail that startup instead of giving way, so whether it runs is a
  decision of the command tree. `TestASecondDeciderFailsTheStartup`.
- **The engine is taken up in `New` and the catalog is not read here yet.** The
  decider decides against the engine alone; the catalog is declared because the
  route-declaration surface that lands with the middleware checks a declared
  permission against it, and a required capability with no provider fails the
  startup whatever callback would read it.
- **The logger and the meter provider are not read by the decider.** No
  decision emits a metric or writes a log line: the per-decision metric
  `authz.guard.decisions` and the refusal log lines belong to the middleware.
- **The root package `pkg/authz` imports the standard library alone, and this
  package is where this directory's gin usage goes.** The handler-internal
  `Guard` API takes a `*gin.Context`, which the root package cannot carry without
  importing gin, so the API is this package's and not the root package's: the
  root package declares no `Guard` and imports no gin, which `deps_test.go`
  holds. The middleware and the `Guard` API land here with the module's HTTP
  face; this package holds the decider and the translation today and imports no
  gin yet. This package reads the engine through the `authz.Enforcer` interface
  and never imports `authz.policy`.
- **A secret, a credential and a policy value reach no log line and no error
  text.** The translation and the refusal say which (object, action) was
  decided and never a grant's rules or a binding's role.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this module alone:

```
go -C pkg build ./authz/...
go -C pkg vet ./authz/...
go -C pkg test ./authz/guard/...
go -C pkg test -race ./authz/guard/...
env -C pkg golangci-lint run ./authz/guard/...
```

The tests ask for no service and no environment. The unit cases
(`decide_test.go`) drive the decider over a stub engine whose answers the case
chooses and whose calls it reads back, with identities built by `authntest`.
The assembly cases drive a real registry through the module's descriptor, with
stand-ins for the engine and the catalog from `authztest`, which is what holds
that `New` takes the engine up, that the registry answers for the seam under
its name, and that a second provider of the seam fails the startup.
