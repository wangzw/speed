# pkg/authn/admin

The administration module of the authentication capability. It mounts the
management routes under the base path's `/admin` tree through `authn.mount`,
decides every one of them at the `Decider` seam, writes the mandatory audit
before a write, and calls the capability the route belongs to.

The descriptor declares the static feature `authn.admin` for the account and
membership groups and one conditional feature per other group
(`authn.admin.sessions`, `.factors`, `.secrets`, `.sso`, `.sso.metadata`,
`.impersonations`). Each conditional name is posted in `Init` in the branch that
mounts its group.

It delivers no capability of its own. It is taken in only by a command that
wants the management routes; without it the account, membership, session,
factor, secret, SSO and impersonation capabilities are still available through
their Go interfaces, and the host's own commands carry their access control.
Taken in without the audit capability, the start fails before any instance is
built.

The API is the package's godoc.

## Boundaries

- **One pipeline, in one order, for every route.** A handler checks that the
  actor is not impersonating, that a `Decider` is there, the shape and the
  tenant, resolves the object's owner into the `Decision`, reads the object,
  asks the `Decider`, checks the actor's strength, writes the mandatory audit
  and only then calls the capability. A read ends after the decision. The
  impersonation check and the missing-`Decider` refusal come before any read,
  so a deployment with no `Decider` tells a caller nothing about which objects
  exist.
- **The account list route's query parameters are a closed set.** A parameter
  the list route does not accept is a 400, answered before the read, so a
  misspelled filter is reported rather than swallowed by a read that ignores it;
  an accepted filter given empty is no filter. The display-name prefix is a byte
  prefix of the stored name and is case-sensitive.
- **The tenant of a tenant-level action is resolved here.** The routes whose
  path carries no tenant (`sso/providers/:id` and its domains and metadata, and
  `impersonations/:id`) read the object's own tenant before asking the
  `Decider`, so it never receives a tenant-level action with no tenant. A path,
  a body and a stored row that disagree on the tenant are a 400 and the
  `Decider` is never asked. `PUT` may not change a provider's tenant.
- **The mandatory audit is written outside any transaction and before the
  action.** A write whose row does not land is answered 503 and the capability
  is not called; a write that fails after it gets a `failed` row linked to the
  permitted one by `action_id`. The impersonation revocation is the one write
  with no mandatory row: ending an impersonation is a move in the safe
  direction and the impersonation module writes its own best-effort row.
- **The groups follow their capabilities.** The account and membership groups
  are always mounted. The session group follows the session store, the factor
  and password group the multi-factor module, the secret group the token
  module, the SSO provider group the SSO module, the metadata refresh group an
  SSO metadata refresher, and the impersonation record group an impersonation
  capability that answers the extended read the routes need. A group whose
  capability is absent is not mounted; the start diagnostic names the groups
  that were.
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
