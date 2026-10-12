# pkg/authz/guard

The authorization enforcement module. It delivers four faces: the decider,
which implements the authentication capability's decision seam `authn.Decider`
and turns a `Decision` into one question put to the policy engine; the
request-path middleware on the authorizing stage, which reads a route's
declaration and refuses a request the policy does not allow; the
route-declaration registry behind `authz.Gate`, which records the permission a
route needs or records it open and holds every declared permission to the
catalog once the registration window closes; and the handler-internal `Guard`
API, which lets a handler ask one more question after the middleware let the
request through. The module declares `authz.guard` and delivers `(*authz.Gate)`,
`(*Guard)` and `(*authn.Decider)`, each exclusively.

The translation from a permission to the (object, action) pair the engine
matches on lives here and nowhere else, because the decider and the middleware
both read it: a permission cannot mean one thing on the request path and another
on the decider's path.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authz.md`](../../../docs/design/modules/design-authz.md) —
  the translation table under "动作到查询的翻译", the middleware's decision table
  under "请求期判定", the decider under "Decider 的动作分派", the platform fallback
  under "平台域与跨租户回退", the route-declaration registry under "类型与接口"
  (`Gate`) and "扩展点", the 403/503 boundary under "错误与失败语义", and the
  module row under "登记单元与名字".
- The decisions behind it, one file per decision, in
  [`docs/adr/`](../../../docs/adr/):
  [`adr-authz-enforcement-2026-10-12`](../../../docs/adr/adr-authz-enforcement-2026-10-12.md)
  (the middleware, the route declaration and the undeclared default),
  [`adr-authz-decision-seam-2026-10-12`](../../../docs/adr/adr-authz-decision-seam-2026-10-12.md)
  (the seam it implements, the one platform fallback, the fail-closed unknown
  action),
  [`adr-authz-packaging-2026-10-12`](../../../docs/adr/adr-authz-packaging-2026-10-12.md)
  (why the decider and the middleware are one module).
- The API itself: the godoc in this package, the seam in `pkg/authn` (`Decision`,
  `Action`, `Decider`), and the declaration surface in `pkg/authz` (`Gate`).

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **The middleware takes the first matching row of the design's decision table,
  in order.** A request with no matched route (`c.FullPath()` is empty) is handed
  on — the entry point answers its own 404. A route declared open is let
  through. A route declared to need a permission goes to the engine. A route
  with no declaration at all is refused under the closed default and let through
  under the open one. The order is what makes the rows total: reading an
  undeclared route before its open or required declaration, or the identity
  before the declaration, changes which requests are refused.
  `TestAMatchedNoRouteIsHandedOn`, `TestAnOpenRouteIsLetThrough`,
  `TestAnUndeclaredRouteIsRefusedByDefaultAndLetThroughWhenOpen`.
- **An undeclared route is refused by default.** `authz.guard.undeclared` is
  `closed` unless the configuration says `open`, and `open` writes a Warn at
  startup. The refusal is a 403 written with the module's problem type and an
  Error line carrying `authz.ErrUndeclaredRoute`, the design's request-period
  sentinel. A policy denial is the only refusal written at Info; the three
  refusals that mean something is wrong rather than someone is refused — an
  undeclared route, a request with no identity where the route requires one
  (the gateway is not in front of the endpoint), and a decision the engine could
  not make — are written at Error. The difference from a denial is in the log
  level, not the response.
  `TestAnUndeclaredRouteIsRefusedByDefaultAndLetThroughWhenOpen`.
- **503 is not a denial.** An engine or store failure is a 503, a policy denial
  is a 403, and neither is ever the other: reading an outage as a denial turns a
  recoverable failure into a permanent permission conclusion. The answer says
  neither which dependency failed nor what the route asked for — the cause goes
  to the log on both the middleware path and the handler-API path — and the
  permission never reaches the body or any response header.
  `TestAnEngineErrorIsA503`, `TestARouteTheEngineDeniesIsA403`,
  `TestGuardRequireWritesTheRefusal`,
  `TestTheDecisionKeepsThePermissionAndCauseOutOfTheResponse`.
- **A request with no identity is answered from the access level.** Where a route
  requires a permission and carries no identity, the gateway's recorded access
  level tells the two cases apart: `AccessOpen` and `AccessOptional` let the
  anonymous request through, everything else is a 401 with the admission layer's
  realm challenge. `AccessRequired` with no identity never reaches this layer —
  the gateway refuses such a request first — so the reachable refusing shape
  reads as `AccessUnknown`. `TestNoIdentityWhereItIsRequiredIsA401`,
  `TestNoIdentityOnAnOpenOrOptionalRoutePasses`.
- **The subject is the actor's accountability key, and the domain is the tenant
  identifier.** The middleware puts `Identity.Key()` — the account whose hands
  are on the request, the actor under an impersonation — and `tenant.FromContext`'s
  identifier, never the alias the request carried, or the platform domain when no
  tenant resolved. `TestTheQueryNamesTheAccountsKeyAndThePlatformDomain`,
  `TestTheQueryNamesTheTenantIdentifierOnTheChain`.
- **`Decide` is a closed translation, and an unknown action is refused.** The
  mapping from a permission to the (object, action) pair is the whole set the
  module can express. An action with no row returns `(false, nil)` — a denial
  and not an error — so an action the authentication side added is refused until
  the table is updated, which is the safe direction; the parity guard in the
  parent directory (`action_parity_test.go`) holds the table and the two
  vocabularies aligned. `ObjectAction` is the one reader of the table, shared
  with the middleware. The middleware treats a declared permission with no row
  the same way: a denial.
- **`authn.tenant.access` is the one action with a platform-domain fallback.** In
  the decider it decides in the tenant's own domain first, and only on a miss
  does it decide again in `PlatformDomain`; two misses are a denial. Every other
  action decides in one domain. The middleware has no such fallback: it decides
  in the request's domain alone. `TestTenantAccessDecidesFourWays`,
  `TestANonAccessActionDecidesInOneQuery`.
- **The handler API only tightens.** `Guard.Allowed` answers a question without
  writing; `Guard.RequirePermission` writes the same 403 a denial gets and the
  same 503 an outage gets, and answers false. Neither reads the declaration
  table, so a handler cannot widen what the middleware allowed. The writing
  method is named `RequirePermission` and not `Require` because the same product
  delivers `authz.Gate`, whose `Require` takes a method and a path: one Go type
  cannot carry two methods of one name, and a `Require` on the handler API would
  collide with it and leave the product unable to deliver both capabilities.
  `TestGuardAllowedAnswersWithoutWriting`, `TestGuardRequireWritesTheRefusal`.
- **A handler may name the domain, and then the request's tenant is not read.**
  `Allowed` and `RequirePermission` decide in the tenant the request resolved to
  (or the platform domain when it resolved none). `RequirePermissionIn` decides
  in the domain it is handed instead, so a management surface whose path tenant
  is data and not the deciding scope is not steered by whatever tenant the
  request happened to resolve to: reading the request's tenant there would let a
  caller authorized in one tenant reach another's data by naming it in the path.
  `TestGuardRequirePermissionInDecidesInTheGivenDomain`.
- **The metric is a closed set.** `authz.guard.decisions` has one attribute,
  `outcome`, whose values are `allowed`, `denied`, `unguarded`, `unavailable` and
  `no-identity`; every collection observes all of them, the empty ones included,
  so a series has a point before its first request. A request that reached a
  decision is counted once; a request that matched no route is not. Renaming a
  value stops a dashboard matching it.
  `TestTheDecisionCountReachesTheMetricCapability`.
- **The module requires the engine and the catalog, and delivers its three
  capabilities exclusively.** The catalog is what the declaration surface checks
  a declared permission against, so a command that brings this module in without
  it fails the startup. The HTTP entry point is taken up in `Init` and is not a
  resolution dependency: a command that guards routes and has no entry point
  fails wiring with `authz.ErrNoRouter`. `Prepare` reads the module's
  configuration and fails the startup on an endpoint list it cannot run on (it
  is required, with no default, and refuses `*`), so the enforcement is never
  silently switched off. The logger and the meter provider are optional: the
  decision reads neither.
  `TestTheGuardNeedsAnEngineAndACatalog`, `TestASecondProviderOfAnyFaceFailsTheStartup`,
  `TestNoEntryPointIsErrNoRouter`, `TestAnIllegalUndeclaredValueFailsPrepare`.
- **A route that declares a permission the catalog does not hold fails the
  startup.** `Start` holds every declaration to the catalog and reports the
  first it does not hold, wrapped around `authz.ErrUnknownPermission` and naming
  the route and the permission. It is a startup diagnostic, not a response, so
  naming the permission does not open the policy-probing path the 403's detail is
  kept clear of. `TestARouteOnAnUndeclaredPermissionFailsStart`.
- **`Prepare` states `StateEnabled` and never `StateAuto`.** `StateAuto` is the
  stance a resolution disables when a second provider claims an exclusive
  capability; this module must fail that startup instead of giving way, so
  whether it runs is a decision of the command tree.
- **The root package `pkg/authz` imports the standard library alone, and this
  package is where this directory's gin usage goes.** The handler API takes a
  `*gin.Context`, which the root package cannot carry without importing gin, so
  the API is this package's and not the root package's: the root package declares
  no `Guard` and imports no gin, which `deps_test.go` holds. The middleware and
  the refusal answers take a `*gin.Context` and so import gin. This package reads
  the engine through the `authz.Enforcer` interface and never imports
  `authz.policy`.
- **A secret, a credential and a policy value reach no log line, no error text
  and no metric.** The translation and the refusal say which (object, action) was
  decided and never a grant's rules or a binding's role; the permission a route
  asked for never reaches the response.

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
(`decide_test.go`, `rig_test.go`) drive the middleware over a real
`*gin.Context` and a stub engine whose answers the case chooses and whose queries
it reads back, with identities built by `authntest`. The assembly cases
(`assembly_test.go`) drive a real registry through the module's descriptor, with
stand-ins for the engine, the catalog and the entry point from `authztest`. The
live cases (`live_test.go`, and one loopback case in `rig_test.go`) run the real
entry point, the real tenant resolution layer and the real admission layer on
loopback ports the system picks, end to end. The configuration cases
(`config_test.go`) drive the real loader over a body through the module's own
schema; the registry cases (`gate_test.go`) drive the declaration table directly;
and the write-out cases (`respond_test.go`) drive the refusal answers over a real
`*gin.Context` on an `httptest` recorder.
