# pkg/authn/admin

The administration module of the authentication capability. It mounts the
management routes under the capability's scope bases through `authn.mount`,
decides every one of them at the `Decider` seam, writes the mandatory audit
before a write, and calls the capability the route belongs to. The account,
session, secret and impersonation-record routes are published in the system
scope (`/api/v1/system`); the membership and identity-provider routes are
published in the tenants scope (`/api/v1/tenants`).

The descriptor declares the static feature `authn.admin` for the account and
membership groups and one conditional feature per other group
(`authn.admin.sessions`, `.factors`, `.secrets`, `.sso`, `.sso.metadata`,
`.impersonations`). Each conditional name is posted in `Init` in the branch that
mounts its group. The SSO provider group has one configuration gate beside its
capability: `authn.admin.sso.enabled` (namespace `authn.admin`, item
`sso.enabled`, bool, default off, primary source only).

It delivers no capability of its own. It is taken in only by a command that
wants the management routes; without it the account, membership, session,
factor, secret, SSO and impersonation capabilities are still available through
their Go interfaces, and the host's own commands carry their access control.
Taken in without the audit capability, the start fails before any instance is
built.

The API is the package's godoc.

## Boundaries

- **One pipeline, in one order, for every route.** A handler checks the
  impersonation rule of the route's decision domain, that a `Decider` is there,
  the shape and the tenant, resolves the object's owner into the `Decision`,
  reads the object, asks the signup gate, asks the `Decider`, checks the actor's
  strength, writes the mandatory audit and only then calls the capability. A
  read ends after the decision. An impersonating identity is refused on the
  platform domain (account, session, secret and impersonation-record surfaces)
  and admitted on the tenant domain (membership, identity-provider and metadata
  refresh surfaces), where the effective account's permissions decide; both
  checks come before any read, so a deployment with no `Decider` tells a caller
  nothing about which objects exist. The tenant-domain groups' routes are
  registered `Impersonable`; the platform-domain groups' are not.
- **A management write by an unverified self-registration is refused.**
  `gateSignup` reads `SignupVerification.SignupStateOf` for the acting
  operator's account and answers `403 contact-required` while the state is
  pending. It runs after
  the object is read and before the `Decider`, the strength check and the
  mandatory audit, so a refused request asks no `Decider`, asks for no strength,
  writes no audit row and calls no capability. Reads are not gated; a service
  account and an account that never self-registered are exempt, and a state that
  cannot be read is a 503 and never a pass. `SignupVerification` is required,
  so an assembly that takes this module in also delivers the account module.
- **The account list route's query parameters are a closed set.** A parameter
  the list route does not accept is a 400, answered before the read, so a
  misspelled filter is reported rather than swallowed by a read that ignores it;
  an accepted filter given empty is no filter. The display-name prefix is a byte
  prefix of the stored name and is case-sensitive.
- **A tenant-addressed route is not automatically a tenant-domain one.** The
  membership and SSO provider routes carry the tenant as their first segment
  and are decided in that tenant's own policy domain; the impersonation-record
  routes are published in the system scope at
  `tenants/:tenant/impersonations` and are the exception, decided in the
  platform domain, with the path's tenant the data the records are confined to.
  `sso/providers/:id` and its domains and metadata carry their tenant too. A
  body that names another tenant than the path is a 400. A stored object that
  belongs to another tenant answers not found, the same answer an absent object
  gets, so the path cannot be used to probe another tenant's objects. `PUT` may
  not change a provider's tenant.
- **The member listing carries roles, the join instant and the next page's
  cursor, and its role read is optional.** Each row carries the tenant's role
  names for the account, ascending and without duplicates, and the membership's
  creation instant in UTC; the page carries `next_cursor`, empty on the last
  page. The roles come from one bulk call to `authn.MemberRoleReader`, not one
  read per row. An assembly with no implementation answers empty role arrays, and
  a read that fails is a `Warn` line and empty role arrays rather than a failed
  page: the member list is the primary task of the surface and a policy outage
  costs it its roles, not its rows. The page is read one row longer than it shows
  below the store's ceiling; at the ceiling the full page is asked again for a
  single row past its last account, because the read cannot exceed its own bound.
  The row carries no owner field: the organization's owner is exposed only on the
  platform tenant detail (`TenantAdminTenant`) and the tenant profile view
  (`TenantProfileView`, behind `tenant.profile.read`), never on a view an
  ordinary member can read.
- **A member write may not suspend its own caller.** `PUT` with `active` false
  and the path's account the effective caller is a 409
  `cannot-suspend-self` (`urn:speed:authn:cannot-suspend-self`), decided in the
  handler after the `Decider` and before the mandatory audit, so the refusal
  writes no row and calls no capability. The membership store's `Put` keeps its
  plain idempotent shape; another administrator may suspend the caller, and
  resuming oneself is no refusal. The last-tenant-administrator guard on removal
  is unchanged and is what keeps a business tenant governable when a removal now
  clears that tenant's bindings.
- **The mandatory audit is written outside any transaction and before the
  action.** A write whose row does not land is answered 503 and the capability
  is not called; a write that fails after it gets a `failed` row linked to the
  permitted one by `action_id`. The impersonation revocation is one exception:
  ending an impersonation is a move in the safe direction and the
  impersonation module writes its own best-effort row. The account deletion is
  the other: its row follows the commit, because the tombstone and the erase
  are the transaction's product and a row written before it would describe an
  action that may roll back. A failed deletion still gets a `failed`
  compensation row, and the answer is the refusal it belongs to.
- **The groups follow their capabilities.** The account and membership groups
  are always mounted. The session group follows the session store, the factor
  and password group the multi-factor module, the secret group the token
  module, the SSO provider group an SSO register that also answers the
  domain-read extended shape its provider views need *and*
  `authn.admin.sso.enabled`, the metadata refresh group an SSO metadata
  refresher, and the impersonation record group an impersonation capability
  that answers the extended read the routes need. A group whose capability is
  absent is not mounted; the start diagnostic names the groups that were. With
  the SSO item off or absent, the provider routes do not exist and neither
  `authn.admin.sso` nor `authn.admin.sso.metadata` is reported, even when the
  register is present.
- **The provider views read domain claims through an extended shape.** Every
  provider view (list rows, get, create, update and metadata-refresh answers)
  carries `domains`: each claimed email domain with its `verified` state, an
  array that is never null and is empty when the provider has none. The list
  reads them in one batch (`DomainsMany`), a single-provider answer in one read
  (`Domains`). Neither is on the published `SSOProviders` interface; the group's
  capability is asserted to the narrow shape that carries them.
- **A provider view and write carry the username strategy.** The view has
  `username_strategy` and `username_claim` as the stored row holds them. The
  create and update bodies accept both, and the capability refuses an
  incomplete or unknown combination; this module passes the two strings through
  unchanged and validates nothing of its own (`providerWriteFields`,
  `providerViewOf`).
- **The impersonation record routes read the capability through an extended
  shape.** They need reading one record by its identifier, which the published
  `authn.Impersonation` interface does not carry, so the group is mounted only
  when the delivered capability also answers it. The `Decision`'s tenant and
  target are the record's own, resolved before the `Decider` is asked.
- **A credential never leaves a request.** An administrator's temporary
  password is read from the body and handed to the password capability; it is
  never echoed, logged or put into an audit detail. A service secret is in the
  creation answer once and in no other.
- **The action names, detail keys and audit tiers are the capability's closed
  set.** A new management route asks a new `Action` (added to the root package)
  and writes the audit action the capability's table names; the detail keys
  are chosen from its closed set, and the three highest-privilege writes (the
  password reset, the factor reset and the impersonation start) carry the
  strong-authentication requirement in addition to the fresh local one.
- **An organization-owner refusal has its own 409.** A deletion or suspension
  the account module refuses with `authn.ErrAccountOwnsOrganization` is mapped
  to `409 urn:speed:authn:account-organization-owner`, with the organization
  identifier in the problem detail. It is distinct from the last-administrator
  refusal so the surface can ask for a transfer or an organization deletion.
- **The settings key read is the multi-factor module's.** This module declares
  no key of its own; it reads `authn.mfa.recent-window` from the global layer,
  with the multi-factor module's failure direction, and a read that cannot be
  made is the declared default.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

`make check` is what CI runs. To run this package alone, from `pkg/`:

```
go test -race ./authn/admin/...
```

The tests need no database: they drive the handlers over a `mounttest.Mounter`
and stand the capabilities in with fakes that count their calls, so a case can
assert that an action that was not authorized, or whose audit did not land, was
never carried out.

The property that the mandatory audit is written outside the transaction has no
test here, and none is possible: this module is a handler that sits on top of the
capabilities and holds no transaction of its own, so the mandatory audit is
written before the capability is called and is never inside a transaction this
module did not open. The property is structural for a handler-only module and is
held where a transaction is actually held — the impersonation module, whose
`Start` opens a record transaction and which carries a SQLite file-database test
for exactly that reason. A management write over a SQLite file database is
exercised by a host's assembly tests, not by this package's.
