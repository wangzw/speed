# pkg/tenant/admin

The tenant capability's management module. It mounts the roster-management routes
under `/api/v1/system/tenants` (platform staff) and the organization-lifecycle
routes under `/api/v1/tenants` (the organization's own owner), and delivers no
capability of its own: a route's action is a call on the roster — reached through
the `tenant.Catalog` capability — except registration, which goes through the
host's `tenant.Provisioner` so a tenant and its first administrator come into
being together, and an owner transfer or organization deletion, which goes
through the host's `tenant.OrganizationWriter`. The module holds no tenant, no
rule and no data of its own. It builds no table, caches nothing and migrates
nothing.

The descriptor declares the static feature `tenant.admin`, present whenever the
module is in force.

The roster owns which tenants exist — what names they carry, which are served —
so this module only decides who may ask and how the answer is shaped. It carries
no policy rule: it asks the decision of the handler-internal `authz.guard` API,
refuses a write from an account whose self-registration has not proved an email
address, and maps the roster's refusals to responses.

Taken in only by a command that wants the management routes, it is absent from
every other command: without it the roster is still read and written through the
`tenant.Catalog` Go interface, and the host's own commands carry their access
control.

The API is the package's godoc, plus the `Catalog`, `Tenant`, `ID` and `Status`
types and the sentinels in `pkg/tenant`.

## Routes and the permissions they are held to

The platform routes are mounted under `/api/v1/system` on the endpoints
`tenant.admin.endpoints` lists; the tenant-surface routes are mounted under
`/api/v1/tenants` on the endpoints `tenant.admin.tenant-endpoints` lists. A route
is addressed by the tenant's name, which a client reads, and an endpoint
layer this module registers resolves it through `Catalog.ByName` under
`/api/v1/system/tenants/`, rewriting the path parameter to the internal
identifier before any module's handler runs. The identifier never appears in a
path or a view. The surface is one
group, `tenants`, and it is always mounted, because the roster it reads and
writes is required. This set is the module's whole route surface.

| Surface | Method and path | Decision |
|---|---|---|
| Platform | `GET /api/v1/system/tenants` | `tenant.read` |
| Platform | `POST /api/v1/system/tenants` | `tenant.manage` |
| Platform | `GET /api/v1/system/tenants/:tenant` | `tenant.read` |
| Platform | `PUT /api/v1/system/tenants/:tenant/owner` | `tenant.manage` |
| Platform | `DELETE /api/v1/system/tenants/:tenant` | `tenant.manage` |
| Platform | `PATCH /api/v1/system/tenants/:tenant/profile` | `tenant.manage` |
| Platform | `POST /api/v1/system/tenants/:tenant/status` | `tenant.manage` |
| Tenant | `PUT /api/v1/tenants/:tenant/owner` | the organization's owner |
| Tenant | `DELETE /api/v1/tenants/:tenant` | the organization's owner |

A platform read route is held to `tenant.read` and a platform write route to
`tenant.manage`. The tenant-surface routes ask no permission: the
`TenantAdminRouteGuard` holds the path tenant to the resolved tenant and refuses
the platform domain, then the handler admits only the account named by the row's
`OwnerAccount`. An owner therefore does not pass through `/system`, and platform
staff who are not the owner do not pass through the tenant surface. Each route's
action is one roster call, one organization write or one profile write:

| Route | Action |
|---|---|
| `GET /tenants` | `List`, every tenant, in the roster's order (by name) |
| `POST /tenants` | `Provisioner`, a tenant, its owner and its first tenant-admin |
| `GET /tenants/:tenant` | `Get`, one tenant by the name the path layer resolved |
| `PUT /tenants/:tenant/owner` | `OrganizationWriter.TransferOwner` |
| `DELETE /tenants/:tenant` | `OrganizationWriter.DeleteOrganization`; `WriteGuard` first |
| `PUT /tenants/:tenant/owner` | `OrganizationWriter.TransferOwner`, the same call |
| `DELETE /tenants/:tenant` | `OrganizationWriter.DeleteOrganization`, the same call |
| `PATCH /tenants/:tenant/profile` | `SetProfile`, the four descriptive fields, and nothing else |
| `POST /tenants/:tenant/status` | `SetStatus`, a new state; a disable asks the host's `WriteGuard` first |

A read answers 200 with a view; a write that creates a record answers 201 with the
record's view, and a write that lands without one (status) answers 204 and
no body.

The two permissions are this module's own: the tenant capability names none, so
this module declares the vocabulary it decides its routes on. It carries
`authz.PermissionSpec` and `authz.TranslationSpec` resources — the two
permissions, and the `tenant`/`read` and `tenant`/`manage` pair each translates to
— so the process's catalog holds them and the process's translation expresses a
decision on either. Without the translation the guard could not express a
permission and every route would refuse its caller. The module also declares an
`i18n.Catalog` resource under `locales/`, one file per locale with the same key
set, carrying the description of each permission it declares: the management
surface renders those texts into the request's language instead of showing a bare
key.

## Boundaries

- **Register goes through the host's `tenant.Provisioner`, and never overwrites.**
  Opening a tenant is not an isolated insert: its creator is the tenant's first
  tenant-admin, so the tenant's `tenant-admin` role, that role's grants and the
  creator's binding have to exist with the roster row. The provisioner writes
  those policy rows first and the roster row last through `Create`, removing
  what it wrote if it fails before the row; a refusal of `Create` stays readable
  through `errors.Is`. An identifier the table already holds is
  `tenant.ErrTenantExists`, answered a 409, and nothing is written. This is
  where `Create` and `Put` divide: `Put` rewrites the row its identifier names,
  the status and the disable source together, which is a seed's action, while
  `Create` only inserts. A name is chosen at creation and never changes; the
  roster has no name-only write, and the management surface has no rename
  route. The request body carries no identifier: the server mints one through
  the shared organization-id generator, and a body that carries an `id` is a
  400. A registration starts the tenant `active` and makes the creator its
  owner and first tenant-admin.
- **The owner transfer has one implementation and two surfaces.** The platform
  route asks `tenant.manage` in the platform domain; the tenant route asks no
  permission and admits only the row's owner. Both call
  `OrganizationWriter.TransferOwner` and take the same tenant lock. The target
  must be an active member whose account is active; the transfer grants it
  `tenant-admin` when it does not hold it, writes the roster owner, and removes
  the previous owner's bindings and membership in one transaction. The platform
  route passes no owner expectation; the tenant route passes the caller as the
  expected owner, so a transfer that commits after the guard read the roster
  answers 409 `ErrOwnerChanged`. The mandatory audit row lands before the write
  with `surface=platform` or `surface=tenant`; a refusal closes it with a linked
  failed row.
- **The deletion has one implementation and two surfaces.** The platform route
  asks `tenant.manage` in the platform domain and passes no owner expectation;
  the tenant route asks no permission, admits only the row's owner, and passes
  the caller as the observed owner, so a transfer that wins the race answers 409
  `ErrOwnerChanged`. An owner does not pass through the platform route unless it
  also holds `tenant.manage`; platform staff who are not the owner do not pass
  through the tenant route. There is no `tenant.delete` permission code. The
  deletion asks `WriteGuard` for `WriteDelete`, lands the mandatory audit row
  naming the surface, and leaves a tombstone with the core relations removed; a
  tombstone read is still a 200 with `status=deleted`.
- **The profile write goes through `SetProfile`, never `Put`.** The body carries
  all four descriptive fields; an empty value clears the stored one. `Put`
  writes the status and disable source too, so a profile update through it would
  carry whatever this process last read and could serve a tenant another replica
  disabled. The profile route reads the row for its 404 first, writes the
  mandatory audit row with `surface=platform`, then calls `SetProfile`. The
  registration's profile fields are optional and go to the provisioner with the
  row; both paths normalize and validate through the root package's
  `tenant.Profile`.
- **One decision order, for every route, read and write alike.** The route's guard
  wrapper reads the caller first: a request with no identity is a 401 — the
  problem the admission and authorization layers write for a route that requires
  an identity and carries none: the status alone, no type, and the realm
  challenge naming the endpoint the route was mounted on — and the wrapper
  returns without asking the guard. On a write it then reads the request's
  actor's self-registration state and refuses an unverified one (see the next
  bullet). Then it asks the guard for the route's permission: a shortfall is the
  guard's own refusal, a 403 for a denial and a 503 for an outage. Only past the
  decision does a write parse its body, run its checks, land its audit and call
  the roster, or a read call the roster and answer. A status write that disables
  the tenant has its shape checked first, then asks the host's
  `tenant.WriteGuard`, and only then lands the audit: an
  answer the guard reads as a denial is a 403 `urn:speed:tenant:protected`, and
  an answer it cannot read is a 503; neither writes an audit row or reaches the
  roster. An enable is not a write the guard protects, so it asks nothing. The
  wrapper sits in front of
  the declared handler because the entry point binds a typed operation's input
  before it calls the handler.
- **A write from an unverified self-registration is refused before the
  decision.** The wrapper reads the request's actor's state through
  `authn.SignupVerification.SignupStateOf` after the caller and before the
  permission decision, the body, the mandatory audit and the roster: a
  `SignupPending` account is answered 403 with the problem type
  `urn:speed:authn:contact-required` (the authentication capability's exported
  `authn.ContactRequiredProblemType`, not this surface's own code) — and the
  action does not run. The subject is the actor and not the account the request
  is taken to be from, so under an impersonation the operator's state is read and
  the target's does not participate. A service account, a static-table
  subject and an account created outside self-registration are exempt, and a read
  route never consults the state. A state that cannot be read is a 503 and is
  never read as verified: the gate only narrows what a caller may do, so a
  failure must not widen it. An account row that does not exist is the one
  exception: it is answered as "no self-registration record" and passed to the
  rest of the pipeline.
  `TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns`,
  `TestAReadIsNotGated`,
  `TestTheTenantReadIsNotGated`,
  `TestAServiceAccountAndAStaticSubjectAreExempt`,
  `TestAnUnreadableSignupStateIs503`,
  `TestTheRegistrationGatePrecedesThePermissionDecision`,
  `TestAnAccountWithNoSignupRecordWrites`,
  `TestTheGateRefusesBeforeTheBodyShape`,
  `TestTheGateReadsTheActorNotTheImpersonationTarget`,
  `TestAnAccountCreatedOutsideSelfRegistrationIsExemptAfterTheRead`.
- **The platform decision is made in the platform domain, whatever tenant the
  request carries.** The roster is the process's own table of which tenants exist and not
  a tenant's, so the handler asks through the guard's explicit-domain form with
  `authz.PlatformDomain` and the request's tenant never steers it: deciding in the
  request's own tenant would let a caller authorized in one tenant reach the whole
  roster by sending a tenant header. Listing the roster is a platform read, not a
  cross-tenant one — the roster's table is not tenant-scoped and its reads are
  outside the filter — so it opens no cross-tenant context and needs no
  cross-tenant permission. The tenant-surface decision is different: the
  `TenantAdminRouteGuard` has held the path tenant to the tenant the request
  resolved to, and the handler admits only that row's owner. Neither surface asks
  the guard for a permission on the tenant route.
- **The mandatory audit of a write lands before the action, outside any
  transaction.** A write's audit row is written before the roster is called; a row
  that does not land is answered 503 and the action is not carried out, so a
  change that could not be recorded is not made. When the roster refuses the
  write, a failed row linked to the permitted one by the action identifier is
  written best-effort: its own failure is an Error line and changes nothing about
  the answer. The action names are a closed set — `tenant.create`,
  `tenant.status`, `tenant.profile.update`, `tenant.owner.transfer` and
  `tenant.delete` — one per write route, and a read
  writes no audit row of its own. A profile update's rows carry
  `surface=platform`; a transfer's and a deletion's rows carry the surface the
  request actually used (`platform` or `tenant`), so the two routes of one action
  are distinguishable.
- **The platform tenant prefix carries the name boundary.** `Init` registers an
  endpoint layer, not a route wrapper, on every endpoint the platform routes are
  mounted on. A request under `/api/v1/system/tenants/` has its `tenant` path
  parameter resolved through `Catalog.ByName` and replaced with the identifier
  the handlers and the policy store key on; a name no tenant carries — including
  an identifier, which is not a name — is the same bare 404 the resolution layer
  writes. Disabled tenants and tombstones resolve, because the platform surface
  manages them. It never touches `/api/v1/tenants/`, whose tenant segment the
  path guard resolves. Because the layer is on the endpoint, it precedes every
  module's handler under the prefix, the authorization and authentication
  management routes included.
  **The system name layer is assembled with `tenant.admin`, which is a host
  constraint.** The layer is registered by this module's `Init`, so a command
  that omits `tenant.admin` mounts no name-addressed platform tenant surface:
  the layer is not in that endpoint's chain, so a route another management
  module mounts under `/api/v1/system/tenants/` reads the name where it expects
  the identifier. A host that wants those routes addressed by name names this
  module in the command; there is no separate layer module to add on its own.
- **An audit row names the tenant by its name.** The write handlers resolve the
  identifier they were handed back to the name through the optional
  `tenant.NameResolver`; an assembly with no resolver writes no tenant name
  rather than the identifier. The registration route names the name it was
  given, and the roster's list order is by name too.
- **The refusal mapping is closed and fail-closed.** A name the roster's
  table already holds — as a name or as an identifier, since the two share one
  namespace — is a 409; an identifier another tenant holds as an identifier or
  as its name is a 409; a name no row carries is a 404; any other failure is a
  503 whose cause stays in the log and never reaches the caller. Identifiers are
  generated by the server at registration; a client never sends one, so the
  identifier half of that one namespace is a fact of the roster and not a value
  a request surface carries. A 404 and a 503 carry no problem type —
  they say something is not there or a dependency could not answer and not which
  refusal it was — while a 409 carries the capability's type prefix
  `urn:speed:tenant:`. A transfer whose target account is not active is a 409
  (`tenant.ErrOwnerInactive`). The write guard's refusal is the capability's third code,
  a 403 `urn:speed:tenant:protected`: the tenant exists and the host declares it
  off-limits to a disable by the operator. `tenant.ErrTenantDisabled` and `tenant.ErrNoTenantInContext`
  are not mapped: they are the resolution and scoping paths' sentinels, and no
  roster write reached from a management route answers them, so one that arrives
  here falls to the same 503 an unknown failure gets.
- **A shape refusal happens after the decision and before the action.** A
  registration whose name is outside the slug domain, a body the handler cannot
  read, and a status outside the closed set
  `active`/`disabled` are 400s and reach neither the roster nor the audit. The
  registration's identifier is generated by the server and judged by the root
  package's `tenant.IsIdentifier` before the provisioner sees it, so an
  identifier outside its domain is this module's own startup-or-request failure
  rather than a client value. The checks use `tenant.IsName` and
  `tenant.IsIdentifier`; this module re-judges no value, it only refuses one
  outside the role the root package defines. A read
  runs no shape check of its own: whether a name names a tenant is the
  roster's to answer, and one it does not hold is a 404.
- **The module decides its own routes in a guard wrapper and declares them
  open.** The middleware lets a management request through to the wrapper, which
  makes the decision in front of the typed handler; the module grants itself no
  permission. Where the assembly delivers the route-declaration surface, `Init`
  registers every management route open on it. With no surface the routes carry
  no declaration, and a guarded endpoint answers them as undeclared — which is
  why a command that mounts these routes should name the enforcement module too.
- **The module delivers no capability and registers `tenant.admin`.** It carries no
  product another module resolves; it is taken in only to mount the routes. It
  states `StateEnabled` and never `StateAuto`: whether the management routes exist
  is a decision of the command tree, not a conclusion a configuration infers. It
  is the framework-provided provider for the roster: the tenant capability itself
  mounts no management face, and a host that wants one writes this module into its
  command rather than hand-writing the same routes, decision and audit preamble.
- **It requires the roster, the handler-internal guard, the audit capability, the
  registration-verification read, the tenant administration route guard and the
  host's three write seams.** A command
  that brings this one in without the roster to act on, the guard to decide
  with, the audit to record through, the `authn.SignupVerification` read to gate
  a write on, the `speedhttp.TenantAdminRouteGuard` to hold the tenant-surface
  path tenant, the `tenant.WriteGuard` to protect a tenant, the
  `tenant.Provisioner` to open a tenant with its first administrator, or the
  `tenant.OrganizationWriter` to transfer an owner and delete an organization,
  fails the startup before any route is mounted, rather than answering every
  route with a refusal or a write that cannot land. The write guard, the
  provisioner and the organization writer are the host's to supply: none is a
  `pkg/` capability, and the roster knows none of them. The tenant
  administration route guard is delivered by `tenant.resolve`. The HTTP entry
  point is taken up in `Init` and is
  required — a command that mounts the management routes and has no entry point
  has nothing to mount on, which is `authz.ErrNoRouter`. The logger is the one
  optional requirement, and the route-declaration surface is taken up optionally
  in `Init`.
- **The answer is a view, built field by field.** Every route answers with a view
  assembled from a `tenant.Tenant` and never the record as it stands, so a field
  added to a record later does not reach a client by being there. The view carries
  the tenant's name, its kind (`business` or `personal`, from the optional
  `tenant.KindResolver`, absent meaning `business`), the four profile fields, its state spelled
  the way `tenant.Status` spells it — `active` or `disabled` — the creation time
  and the owner account read from the roster row. The creation time is always
  present: `created_at` is not null, so the view renders it as an RFC 3339
  instant in UTC and carries no omission branch. The owner account is always
  present too, empty for a tenant with no owner, such as the platform's own
  tenant. This view is the only platform-domain place the owner is exposed
  alongside `TenantProfileView`; the member list and the self tenant view carry
  no owner field.

## Configuration

- **`tenant.admin.endpoints`** (configuration, a list of strings): the endpoints
  the platform management routes are mounted on, by the names the entry point's own
  configuration gives them. It is required and has no default — a management
  surface mounted nowhere would silently drop every route — and it takes concrete
  names only, never `*`, because mounting the routes on every endpoint would put
  them on the public ones, where no gateway guards them. The item takes the
  primary source alone. A shape the module cannot run on — an empty list, an empty
  name, `*`, a name written twice — is `tenant.admin`'s `ErrInvalidConfig`; a name
  the entry point does not declare is `authz.ErrUnknownEndpoint` at startup.
- **`tenant.admin.tenant-endpoints`** (configuration, a list of strings): the
  endpoints the tenant-surface owner-transfer and organization-deletion routes are
  mounted on. Unset means `["api"]`. It takes concrete names only, never `*`, and
  an endpoint it names must carry tenant resolution and the tenant administration
  route guard. The same shape checks and failures apply.

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
go -C pkg build ./tenant/...
go -C pkg vet ./tenant/...
go -C pkg test ./tenant/admin/...
go -C pkg test -race ./tenant/admin/...
env -C pkg golangci-lint run ./tenant/admin/...
```

The tests ask for no service and no environment. The route cases mount the
module's whole route set on a real `gin` engine and drive it over an `httptest`
recorder, with stand-ins for the roster, the guard, the audit, the signup-state
read and the configuration reader, and a real identity from `authntest` — so a
case can assert that a route the guard refused, a write the registration gate
refused, a request with no identity, and a write whose mandatory audit did not
land reached no roster call and wrote no audit row. The
assembly cases drive the module's descriptor through a real registry, which is
where what it requires and takes up is observed; one of them drives the real
enforcement module alongside this one, which is what proves this module's
translation rows reach the process's one set without a conflict.
