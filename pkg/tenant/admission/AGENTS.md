# pkg/tenant/admission

The tenant admission module: one middleware layer per listening endpoint, deciding
whether an identified caller may enter the tenant its request resolved to. It
answers the three questions neither neighbour can. Authentication knows who the
caller is and not which tenant the request is for; tenant resolution knows the
tenant and does not read the caller. This layer reads both — whether the account
is a member of the tenant, whether it meets the tenant's own requirements of how
its members authenticate, and whether an impersonating identity is confined to
this tenant — and it never answers 403, because what a member may do once inside
is the permission model's.

It is a module of the tenant directory rather than of the authentication one:
it reads an identity and a tenant context at once, and the authentication tree
imports nothing of the tenant tree. It reaches the identity system through the
authn root package's interfaces alone.

The root package is the implementation. It registers the module `tenant.admission`,
delivers `(*tenant.Admission)(nil)`, the ordering anchor, and
`(*authn.ScopeGuard)(nil)`, the confinement guard, each exclusively, and declares
one configuration item and three settings items under the `tenant.admission`
namespace. `pkg/tenant`'s root package holds the `tenant.Admission` interface;
`pkg/authn`'s holds `authn.ScopeGuard`.

The API is the package's godoc. Read `Module`'s comment before changing the
descriptor, and `decide`'s before changing the judgement order.

## Boundaries

- **The marker method is exported, and that is load-bearing.**
  `tenant.Admission` is `interface{ AdmissionLayer() }`, defined in `pkg/tenant`
  and delivered by a type in this package. A type of another package cannot
  implement an interface whose method is unexported, so an unexported marker
  would fail the registry's delivery check for the one implementation there is
  (`TestTheMarkerMethodIsExportedSoAnotherPackageCanDeliverIt`). The interface is
  an ordering anchor and not a surface: a layer that has to sit inside admission
  names `(*tenant.Admission)(nil)` in its `After` and calls nothing on it. The
  godoc in `pkg/tenant/capabilities.go` states the same reason.
- **The layer's `Provides` carries the token the descriptor delivers under.** The
  middleware's `Provides` is `(*tenant.Admission)(nil)`, the same token the
  descriptor delivers, and it is the only anchor another layer has for placing
  itself inside or outside admission, so the two must not drift
  (`TestTheMiddlewareDeclaresWhatTheDesignSays`).
- **The layer declares the authorizing stage and no constraint of its own.** The
  stage runs after identity and tenant are settled, which the layer reads, and
  inside the recording and the throttling stages, so a refusal made here is
  audited and counted by the stage order rather than by a preference. Assemblies
  that leave the audit capture or rate limiting out are therefore placed the same
  way
  (`TestTheLayerSitsInsideResolutionAuditAndRateLimitingAndOutsideAuthorization`,
  `TestTheStageOrderPlacesAdmissionWithoutTheOuterStages`).
- **The endpoint list is required, refused as a set, and never `*`.** There is no
  default: guarding every endpoint would cover the endpoint the authentication
  routes are mounted on, where a request that ends an impersonation has no tenant
  and would be answered 404, so a module that cannot answer fails the startup
  naming the key. An empty list, an empty name or `*` is `authn.ErrInvalidConfig`;
  a name the entry point does not know is `tenant.ErrUnknownEndpoint`; a name
  written twice is `tenant.ErrDuplicateEndpoint`; no router at all is
  `tenant.ErrNoRouter` (`TestNoDefaultForTheEndpointList`,
  `TestPrepareFailsOnAnEndpointListItCannotRunOn`,
  `TestPrepareFailsOnAnEndpointNamedTwice`, `TestPrepareFailsWithoutTheEndpointList`,
  `TestInitFailures`). The list is read from the primary configuration source
  alone (`TestTheEndpointListIsReadFromThePrimarySourceAlone`).
- **`Prepare` states `StateEnabled` and never `StateAuto`.** `StateAuto` is the
  cell the resolution of an exclusive capability disables when a second provider
  claims it; this module must fail that startup instead of giving way, so whether
  the layer runs is a decision of the command tree
  (`TestPrepareStatesEnabledNeverAuto`, `TestTwoAdmissionLayersFailTheStartup`).
- **The judgement order is fixed, and several guarantees are properties of
  it** (`decide`). Strength rows come after the membership row, so a caller that is
  not a member never meets a prompt only a member would meet and every refusal of a
  non-member is the same 404 (`TestStrengthComesAfterMembership`, the `TestRow*`
  cases). An impersonation and a service account still go through the membership
  row, which is what makes a removed member's impersonation stop working. The
  account an impersonation is checked for is the impersonated one — `Account()` is
  the effective account — so removing the member ends the impersonation
  (`TestAnImpersonationIsKeyedByTheImpersonatedAccount`). A non-member the
  permission model let in leaves the judgement there: the tenant's strength
  requirements bind the tenant's members, not the platform role that was let past.
- **Every 404 is the same bytes, and 403 is not in the status set.** The three
  404 paths — an impersonation with no tenant, a scope that does not match the
  tenant, a non-member — are answered with the status and nothing that tells them
  apart, so there is no tenant-enumeration oracle
  (`TestEveryNotFoundIsTheSameBytes`). The status set is closed at 404, 401 and
  503 (`TestTheStatusSetIsClosed`); the answer goes through the entry point's
  `Fail` with the status as the only member of the `Problem`, and a step-up answer
  sets its `WWW-Authenticate` first.
- **The step-up challenge carries the identity's own scheme and no `max_age`.**
  The scheme is `Identity.Challenge()`, which the gateway stamped from the
  credential's carrier, and a value that is not an header-field token falls back
  to `Bearer` so a header is never ended by the stamped value; there is no
  `max_age` because this layer compares a level and a provider and has no
  freshness requirement to state (`TestTheChallengeNeverCarriesWhatEndsAHeaderField`).
- **The effective assurance level confines an IdP's declaration to its own
  tenant.** A multi-factor level `AssuranceFromIDP()` vouches for counts as
  multi-factor only where the provider belongs to the tenant under judgement;
  elsewhere, and where the registry of providers is absent, it counts as one
  factor (`TestEffectiveAssurance`).
- **The cache remembers answers and never errors.** A failed read is not an
  answer, and remembering one would turn an outage of a second into a refusal of
  the lifetime. The three kinds — membership, SSO exemption, permission — are kept
  apart because they answer different questions about one key
  (`TestAnErrorIsNeverRemembered`, `TestThePermissionIsRememberedAndAnErrorIsNot`,
  `TestTheSSOExemptionIsRemembered`). A `authn.membership.changed` event only
  drops: it drops that account's entries in that tenant, or everything when it
  cannot be read down to a key, and the handler is idempotent
  (`TestAMembershipEventDropsThatAccountInThatTenantAndNothingElse`,
  `TestAMembershipEventDropsEveryKindOfEntry`, `TestAMembershipEventIsIdempotent`).
  A read that straddles an invalidation must not leave its answer behind, which is
  what the cache's epoch guards (`TestAnAnswerReadWhileAnEventArrivesIsNotLeftBehind`).
  The lifetime bounds staleness whether or not an event arrives; without a bus the
  lifetime alone does (`TestTheSubscriptionIsMadeInInitAndWithoutABusTheLifetimeAlone`).
- **A settings read goes by the declared path, and the two switches are
  requirements while the lifetime is an adjustment.** A switch that cannot be read
  is 503 and never the permissive default; the lifetime that cannot be read, or
  holds a value outside its domain, falls to the declared default and writes one
  Error line, once per distinct fault
  (`TestTheLayerReadsTheSwitchesTheStoreHoldsPerTenant`,
  `TestTheLifetimeIsReadFromTheGlobalRowAlone`,
  `TestALifetimeTheCodeCannotActOnIsTheDefaultAndIsReported`,
  `TestTheLifetimeFaultIsReportedAgainOnceItHasBeenCleared`, `TestSettingsFailures`).
  A switch's effective value is the global row OR the tenant's own row: the
  global layer is the platform floor, and a tenant can raise it but not lower
  it. The declared default applies only where neither layer holds a row. The
  lifetime is read from the global row alone. Without a settings store each
  switch reads as off, which is the declared default, and the requirement cannot
  be turned on in that assembly.
- **A tenant identifier is checked before it is handed to the settings.** The
  settings refuse an empty identifier and one with a blank or control character by
  panicking, and a request must not take that panic, so `nameableLayer` refuses
  such an identifier as an error first
  (`TestNoTenantIdentifierTheSettingsRefuseReachesThem`).
- **The two switches are tenant-facing; every other item under this namespace is
  not.** `require-mfa` and `require-sso` carry `TenantReadWrite` on a schema that
  is `TenantFacing`, so the tenant surface may write the tenant's own row under
  `settings.tenant.write`; the global row is the platform floor, and the judgement
  reads the OR of the two, so a tenant write can only raise the requirement. The
  tenant surface additionally refuses a write that would disable a
  platform-enforced switch with 409 `tenant-admission-enforced`; that refusal is
  an experience protection, and the OR is what keeps the requirement on when a
  tenant row of false predates the platform's switch. `check-ttl`, and every
  `authn.*` setting, stay platform-only: the layer neither blocks a relaxation
  made through a surface it does not own nor detects it, and the settings.changed
  subscription reports one after the fact.
- **The SSO row exempts the tenants the configuration names.** `sso-exempt-tenants`
  lists tenant identifiers the row does not bind; their members are still checked
  for membership and still held to the MFA row, but they are not asked for a
  sign-in through the tenant's own identity provider. The platform's own tenant is
  the case the item exists for: it hosts the console, has no provider of its own,
  and would otherwise refuse every request there once a deployment switches the
  row on (`TestRowSSO`, `TestResolveCarriesTheSSOExemptTenants`).
- **Every diagnostic line starts with `tenant: ` and names the module.** The
  capability directory's prefix leads the line so the several writers of the
  startup diagnostics can be told apart. A registry of identity providers that is
  absent while `tenant.admission.require-sso` can be switched on in the settings
  writes one Warn, because a tenant that switches it on has every request refused
  (`TestStartupWritesTheGuardedEndpointsAndEveryLineStartsWithTheDirectoryPrefix`,
  `TestTheWarningForAnAbsentRegistryOfProviders`).
- **The metric counts every request once, under a closed set of outcome names.**
  `tenant.admission.decisions` is observed on every collection, the empty outcomes
  included, so a series has a first point before its first request; each request
  adds exactly one, under the outcome it got
  (`TestEveryOutcomeIsCountedOnceAndOnlyOnce`, `TestTheOutcomeNamesAreTheDocumentedClosedSet`,
  `TestTheDecisionCountReachesTheMetricCapability`). The instrument is created on
  the `tenant` meter. A name or an attribute value is a compatibility break for a
  dashboard written against it.
- **No secret reaches an answer or a log line.** The refusal line carries the
  account, the tenant and the class of the cause alone, through the request's own
  logger so it also carries what the layers outside bound on it
  (`TestNoSecretReachesAnswerOrLog`).
- **The layer reaches the identity system through the authn root package alone.**
  It imports no implementation subpackage of that tree and no package of the
  tenant tree beside the root, reaches no database and no key-value primitives;
  memberships are read through the interface and the tenant through the root
  package's context (`TestTheLayerReachesTheIdentitySystemThroughItsRootPackageAlone`,
  `TestTheLayerKeepsToTheLibrariesTheDesignNames`). No package of the authn tree
  imports this layer (`TestNoPackageOfTheAuthnTreeImportsTheLayer`).
- **The callbacks act on this module's own product and on nothing else.**
  `productOf` recognises the one type this package builds; an instance of any
  other type is a host's own, supplied for the module, and the callbacks leave it
  alone — no layer registered, no event subscribed to, and what it holds is
  released by whoever supplied it. Taking whatever carries the product's methods
  for the product is the shape to reject: a wrapper a host writes around the
  product carries every one of them
  (`TestACallbackActsOnTheProductAndNothingElse`,
  `TestASuppliedInstanceThatIsNotTheProductIsLeftAlone`).
- **The optional capabilities are taken up through one helper that tells absence
  from failure apart.** Absence is the legal configuration and leaves the zero
  value; any other failure stops the startup, because leaving the capability out
  would change what the layer enforces without a word
  (`TestEveryOptionalCapabilityIsTakenUp`, `TestEveryOptionalCapabilityMayBeAbsent`).
- **`ScopeGuard.Covers` is an exact membership test.** The endpoint list holds
  concrete names only, so the answer is a set test and never a pattern match, and
  a name that is not listed is not covered however it is spelled
  (`TestCoversIsAnExactMembershipTest`). The layer does not check that a host left
  tenant resolution off a route on an endpoint it guards, and it does not check
  that the authentication routes' endpoint is not among the ones it guards: the
  gateway checks the latter in `Start`
  (`TestTheEndpointTheAuthenticationRoutesAreMountedOnIsNotOneTheLayerGuards`).
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort `authn.settings.relaxed` audit row naming the key and the
  direction and no value; the settings it watches are `require-mfa` and
  `require-sso` in the `to-false` direction and `check-ttl` in `increase`
  (`relax.go`, `TestARelaxingChangeWritesAWarnAndAnAuditRow`).

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

CI runs the segments of `make check` as parallel jobs. On this module alone:

```
go -C pkg build ./tenant/...
go -C pkg vet ./tenant/...
go -C pkg test ./tenant/...
go -C pkg test -race ./tenant/...
env -C pkg golangci-lint run ./tenant/...
```

The tests ask for no service and no environment. The layer's own cases
(`rig_test.go`) drive its handler with each collaborator a stub whose answers the
case chooses and whose calls it counts, on a clock the case moves; the identity
is built by `authntest`, and a count key a case expects is read off the identity,
never written down. The assembly cases (`assembly_test.go`) drive a real registry
through the descriptor — the configuration module with a reader the real loader
produced, the entry point module itself, this module, and stand-ins for what it
takes — which is what holds the step each callback runs at and what the registry
says of the descriptor. The end-to-end cases (`live_test.go`) run the entry point
module itself on a loopback port the system picks and read the response off the
socket, with the gateway in front, so the layer is exercised in a chain.
