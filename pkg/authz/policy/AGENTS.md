# pkg/authz/policy

The authorization policy module `authz.policy`. It owns the policy tables and
their migrations, the thin store the engine reads and writes through, the
Casbin engine snapshot the decisions read, the permission catalog with the
translation beside it, the platform and host seeding surfaces over the policy
data, and the account lifecycle cleanup. It delivers `authz.Enforcer`,
`authz.SubjectPermissions`, `authz.Catalog`, `authz.Manager`, `authz.Seeder`,
`authz.Reconciler` and `authz.TenantAdminCounter`, each exclusively, the
transaction-aware `TxManager` beside `Manager`, and implements
`authn.AccountLifecycle`, `authn.MembershipLifecycle` and the last-administrator
`authn.MembershipGuard` (a host may add guards beside it; every guard is asked
and any refusal refuses). It also implements the optional `authn.MemberRoleReader`
the member listing reads: one bulk answer of each account's role names in a
tenant, built over the manager's own binding listing, and delivered beside the
lifecycle hooks rather than exclusively. The invitation capability's two seams
are delivered the same way: `authn.InviteRolePolicy` answers whether an account
may grant a role and whether an invitation's role may still be bound (both over
`Manager.RoleSubsetHeld`, the first also asks the host `WriteGuard`, and the
second also reads the inviter's active membership through the assembled
`authz.MembershipChecker`; without that checker an account-issued role-bearing
invitation is refused at both ends, so an invitation that could never be
accepted is never written), and
`provisioning.RoleBinder` writes a binding inside an acceptance's transaction.
`Manager.RoleSubsetHeld` is the one implementation of the anti-escalation subset
rule the tenant management surface and the invitation judgement share. `SubjectPermissions.Held` reads the same snapshot a
decision reads and maps each held (object, action) pair back through the
catalog's reverse lookup; a pair no declared permission translates to is left
out of the answer.

The catalog carries two readings of its permissions. `Declared` is every
permission a registered module declares; `TenantGoverned` is the subset a tenant
administrator may hold and grant, marked by each module in
`PermissionSpec.TenantGoverned` and defined centrally for this capability's own
permissions. `platform-admin` exists only in the platform domain and is granted
`Declared`; on a tenant surface it admits nothing by itself. A business or system
tenant's `tenant-admin` is granted `TenantGoverned`; a personal tenant's is
granted the host's business-resource set intersected with `TenantGoverned`, minus
the host's excluded set; it is not seeded as a builtin and is never converged by
`Reconciler`. `Governed` answers for one permission. `Metadata` answers a
permission's governance metadata — the group a management surface files it under
and whether granting it is dangerous — from the declaring module's
`PermissionSpec.Metadata`; a declared permission the module gave no metadata is
the zero value, and an undeclared one answers false.

Resolving the process's permission translation belongs here as much as the
catalog does: the two are built over the same registered modules, and a host
command that writes policy data in a permission's name — a bootstrap that seeds
a role — takes this module in and reads the (object, action) pair a permission
translates to through `authz.Catalog.ObjectAction`, without the enforcement
module and without the HTTP entry point that module requires. The description key
each module gave a permission is kept beside the catalog and read through
`authz.Catalog.DescriptionKey`; this module hands out the key, and rendering it
is the i18n engine's.

The Casbin engine and the database it reads live here and nowhere else, so a
host that only guards routes carries no policy store. The thin adapter and the
snapshot holder are the package's own units under `pkg/authz/internal/`, which
only this directory can import.

The API is the package's godoc and `pkg/authz`'s. Read the `Manager` and
`Enforcer` comments before writing a consumer.

## Boundaries

- **The migration files are the schema.** They are declared as one
  `db.Migrations` resource and applied by the database module; the module
  declares no `Migrate` callback of its own. Every table leads its key with
  `tenant_id`, a real non-empty column, so the tenant filter and the scoped
  models recognise the tenant rather than reading it out of a policy string.
  The deciding process' correctness turns on this: with a key that named a
  non-tenant column first, one tenant's write would collide with another's row
  and rewrite its tenant, and the statement names no other tenant to refuse.
  `migrate_test.go` runs the shipped files on SQLite and PostgreSQL and asserts
  the keying and the non-empty column; a table shape written out in a test
  rather than read from the file proves nothing. `0003_role_description.sql`
  adds the role's `description` column and `authz_bindings`'s
  `(tenant_id, role, account_id)` index; it deletes no row.
- **The model text is fixed in the root package, and its self-check runs before
  the process serves.** `ModelText` is the one model the engine is loaded with;
  a host writes policy data and never the model. A wrong matcher does not fail
  to load — it silently allows or denies where the model intends the opposite —
  so `Start` runs the root-package fixtures on the exact text the process will
  enforce with, and a failure stops the startup. The reload in that path is
  bounded by the configured `authz.policy.load-timeout`; a load that does not
  finish fails the startup rather than installing a half-read snapshot.
- **The snapshot is immutable and replaced as a whole.** `Enforcer.Allowed`
  reads the atomic pointer and runs the matcher without a lock; the reload
  swaps the pointer and never mutates a snapshot in place, so a reader takes
  the old snapshot or the new one and never a mixture. The write order is fixed:
  the row is committed, then this replica swaps its pointer, then it publishes
  `authz.policy.changed`. The local swap does not wait on the event, and a
  failed publish is logged rather than returned — the row is already in the
  table and the other replicas reload at their next tick or a later change.
- **The table is written one row at a time, never rewritten wholesale.** A
  `Manager` write commits in its own transaction, and a write that checks a role
  first — a grant, a binding, a role rewrite, a delete — does that check and the
  row it guards in the same transaction, so a concurrent writer cannot land
  between them and leave a grant or a binding on a role that was just removed or
  turned builtin. The check reads the role under its own row lock (`SELECT ...
  FOR UPDATE`), which is what makes the guarantee hold under `READ COMMITTED`: a
  delete takes the same lock, so the two serialise and the later one reads what
  the earlier committed rather than a stale present-row. The lock is inert on a
  dialect without row locks, where the single writer already serialises the
  writes. The thin adapter's `SavePolicy` is left unimplemented because a
  clear-and-rewrite would drop a write that landed between its read and its
  write. A host that wants a different store shape is the shape to reject.
- **`TxManager` joins a caller's transaction.** `PutRoleTx`, `DeleteRoleTx`,
  `SeedGrantsTx`, `PutBindingTx`, `ClearAccountBindingsTx` and
  `DeleteTenantPolicyTx` are the same writes inside a `*gorm.DB` the caller
  already holds — an account's creation, where a new organization's roles,
  grants and bindings are written in the account's transaction, an ownership
  transfer's administrator binding, an organization deletion's policy teardown,
  and an invitation's role binding. They write through that handle and nothing else, and
  return the snapshot reload and change event as a closure for after the
  caller's commit: a reload inside the transaction would not see the write, and
  the event would announce a change that may still roll back. The capability is
  declared in this package rather than in `pkg/authz`, whose closure may hold
  neither gorm nor a subpackage of the directory; it is delivered exclusively
  beside `Manager`, and the two share their checks and refusals.
- **The store is read and written through the adapter's construction, not per
  call.** The `persist.Adapter` methods carry neither a context nor a tenant, so
  the adapter binds both once in `New`: the context a load runs under, marked
  cross-tenant with `tenant.CrossTenant` so that a load reads every tenant's
  rows, and the caller a write is checked against. A write whose rule names
  another tenant is refused with `authz.ErrTenantMismatch`; the cross-tenant
  mark says the call intends to reach every tenant, not that any row may be
  written into any tenant. `LoadPolicy` is this module's one cross-tenant read
  of a security-sensitive table and each replica holds every tenant's policy;
  isolation rests on judging only within the request's domain, not on the
  process holding one tenant's data.
- **`Manager` refuses the writes below.** A grant or a binding on a
  role that was not created in the tenant is `authz.ErrUnknownRole`; deleting a
  role, rewriting it, or changing a builtin role's grants is
  `authz.ErrBuiltinRole`; a write that names no tenant is
  `authz.ErrTenantMismatch`, and a binding that names no account is refused too.
  A builtin role may still be bound — that is how the platform operations'
  roles are handed to an account.
- **`Seeder` grows a builtin role's grant set and never shrinks it.** It is
  delivered exclusively beside `Manager` and takes
  `SeedGrants(ctx, tenant, role, grants []authz.Grant)`: it adds the
  (resource, action) pairs the role does not hold, including on a builtin
  role, and reports how many it added. A held grant is no work, no error and no
  count, so a repeated seed over an unchanged set writes nothing, reloads
  nothing and publishes nothing. It never removes, rewrites or re-timestamps a
  grant. An absent role is `authz.ErrUnknownRole`, an empty tenant
  `authz.ErrTenantMismatch`, and the role check and the inserts run in one
  transaction with the role's row lock, the same serialisation the `Manager`
  writes take.
- **`Reconciler` is the only write that removes a builtin role's grants.** It
  is delivered exclusively beside `Seeder` and takes
  `ReconcileGrants(ctx, tenant, role, grants, retired []authz.Grant) (added,
  removed int, err error)`; the two capabilities share the `authz.Grant` input
  type. It applies to one domain's builtin `tenant-admin`: it
  adds what the target set holds and the role does not, and removes the
  intersection of the role's grants with the caller's retired set. When the
  target set and the retired set overlap, the target set wins and a grant in both
  is kept. It never infers a removal from the catalog — a grant outside the
  target but not listed
  as retired stays — so replicas on different versions do not delete each
  other's grants. The error order is fixed: a role other than `tenant-admin` is
  `authz.ErrReservedRole` before any read, an absent `tenant-admin` is
  `authz.ErrUnknownRole`, and a `tenant-admin` that is not builtin is
  `authz.ErrReservedRole`; all three precede any convergence write. The add and
  the remove run in one transaction under the role's row lock; a failure rolls
  back every row of the call, and a retry reconverges. After the commit the
  module reloads the snapshot and publishes `authz.policy.changed` when anything
  changed, and a no-change call returns zero without either. A failure is
  retryable: add-then-remove leaves a superset on an interrupted call, which the
  next call converges. A personal tenant's `tenant-admin` is not builtin, so it
  is refused. The host's seeding step writes the mandatory audit row
  before the call; an audit that cannot be written means no reconciliation. The
  seeding step is the only caller; neither management module takes it up.
- **The builtin mark has one write, and `PutRole` is not it.** `PutRole`
  refuses a record carrying `builtin` with `authz.ErrBuiltinRole`, whether it
  would create or rewrite the role: a role created with the mark would be
  frozen before the grants that give it meaning are in place, and a rewrite
  that could add it would turn any role into one that can be neither rewritten
  nor deleted, with no path back. `MarkRoleBuiltin(tenant, role)` sets the mark
  on an existing role — the host's seeding step calls it once the grants are in
  place — and is idempotent; an absent role is `authz.ErrUnknownRole`.
- **A role carries a free-text description beside the builtin key.** The
  `authz_roles` row has two description columns: `description` is the operator's
  free text and `description_key` is the i18n key a host seeds into a builtin
  role. `PutRole` writes both, and a management surface's PUT writes
  `description` alone; the host's seed keeps writing the key. The read path
  prefers a builtin role's key (rendered by the caller's localizer) and returns
  every other role's free text as written. `RoleDescriptionMax` is the length a
  management write is held to; over it is the caller's 400, not this module's.
  The module's own role catalog (`rolelocales/`) holds the builtin role
  descriptions the host's seed keys name, registered as an `i18n.Catalog`
  resource so they resolve in every request's language.
- **The role-usage reads are one grouped count and one cursor page.**
  `GrantCounts` answers how many grants each role holds from one grouped read; a
  role holding none is absent and reads as zero. `BindingsOfRole` answers one
  page of a role's bindings in account order, with an exclusive account cursor
  and an optional limit, for the role-usage page. Both are `Manager` methods,
  not separate capabilities: the two management surfaces already hold the
  `Manager`.
- **The identity behind a usage row is the host's.** `AccountIdentityReader` is
  an optional root-package seam: a management surface resolves the accounts its
  usage read names to a display name and an activity state. This module neither
  implements it nor reads the account table; absent it the caller reports an
  empty name and an unknown activity, and an unreadable answer is the caller's
  503.
- **Deleting a role revokes it.** `DeleteRole` removes the role's row together
  with its rows in `authz_grants` and `authz_bindings`, in one transaction. The
  engine decides from those two tables and never reads `authz_roles`, so the
  three rows are one unit: a leftover grant or binding would keep deciding for a
  role that no longer exists. Another tenant's same-named role, and its rows,
  are untouched.
- **The platform-domain check runs only in an assembly that has a roster.**
  `Start` reads the roster for a tenant whose identifier is `PlatformDomain` and
  fails the startup with `authz.ErrPlatformDomainMissing` when none exists: the
  platform domain's policy rows are that tenant's, so an assembly with a roster
  and no such tenant has nowhere for a platform decision to land. A policy
  module with no tenant module has no roster and runs no such check, and that
  absence is legal.
- **The administration roles are bounded by domain, and a domain keeps an
  administrator.** `PutRole` and `PutBinding` refuse `PlatformAdminRole` in any
  domain but the platform domain with `authz.ErrReservedRole`;
  `TenantAdminRole` belongs to every domain. A platform administrator binding
  asks the optional `authz.MembershipChecker` about the account in the platform
  tenant and refuses with `authz.ErrMissingMembershipChecker` when the assembly
  supplied none, so the check fails closed; a checker that answers "not a
  member" refuses with `authz.ErrPlatformMembershipRequired`. `DeleteBinding`
  refuses removing the last `TenantAdminRole` binding of a domain with
  `authz.ErrLastTenantAdmin`; it takes the host's `authz.TenantLock` before the
  count and releases it after the commit, and the membership guard takes the same
  object, so a binding delete and a membership removal cannot each see enough
  bindings and together leave the domain with none. `ClearAccountBindings` is the
  host's account-deletion path and the membership-removal cascade beside
  `DeleteBinding`: it clears one account's bindings in one tenant, skips the
  last-administrator rule, which protects a domain still in service, and takes no
  lock, because either caller already holds it and re-entering would deadlock.
  `TestPutRoleRefusesThePlatformAdministratorRoleOutsideThePlatformDomain`,
  `TestPutBindingOfThePlatformAdministratorNeedsAMembershipChecker`,
  `TestPutBindingOfThePlatformAdministratorChecksMembership`,
  `TestDeletingTheLastTenantAdministratorBindingIsRefused`,
  `TestClearAccountBindingsClearsOneAccountWithinOneTenant`,
  `TestABindingDeleteWaitsForTheMembershipRemovalLock`.
- **The account lifecycle cleanup is idempotent and crosses tenants.** A
  deleted account's binding rows are removed across every tenant; running the
  cleanup again is no work and no error. A binding row that outlives the
  deletion names an account the authentication side can no longer produce, so
  the account is unusable there whether or not the row arrived. The host's
  per-tenant teardown has `Manager.ClearAccountBindings` beside this hook; the
  hook clears every tenant, while the manager method names one tenant and one
  account.
- **The tenant-administrator counter reads across tenants and defers the kind
  question to the host.** `SoleTenantAdminTenant` counts each tenant's
  tenant-administrator bindings and names the first active tenant where the
  account holds the only one. The roster supplies "in service"; the optional
  `authz.WriteGuard` supplies "business tenant", because a personal tenant is
  the host's policy and this capability does not infer it from an identifier.
  The platform domain, a tenant the roster does not carry or does not serve, and
  a domain the guard refuses are left out. A missing roster or guard failure is
  an error, never "not sole":
  `TestSoleTenantAdminTenantFindsTheOnlyBusinessAdministrator`,
  `TestASecondTenantAdministratorAllowsTheDeletion`,
  `TestAPersonalTenantIsNotCounted`, `TestADisabledTenantIsNotCounted`,
  `TestTheCounterFailsClosedWhenTheRosterCannotAnswer`.
- **The counter also locks the account's tenants, for the deletion's check and
  cleanup.** `LockAccountAdministration` takes the host's `authz.TenantLock` for
  the union of two sets, in identifier order, and answers one idempotent release;
  a lock that cannot be taken releases the ones already held. The first set is
  every tenant the account's tenant-administrator bindings name, not only the
  serviceable ones: the lock excludes every other removal that judges those
  floors, and a status change must not open a hole in that exclusion. The second
  set is every active business tenant the account is an active member of, read
  through the optional `authn.Memberships` seam: an ownership transfer makes an
  active member the owner, so a suspension or deletion that locked only the
  administered tenants could interleave with that transfer and commit while the
  account becomes an owner. A personal or disabled tenant, the platform domain,
  and a guard that cannot answer keep the fail-closed behaviour the counters
  share. This is the same lock `Manager.DeleteBinding`,
  `MembershipGuard.Lock` and `tenant.ownership`'s transfer take, so a deletion, a
  binding removal, a membership removal and a transfer cannot interleave across
  the check.
  `TestTheAccountAdministrationLockCoversAMemberOnlyAccount` holds the
  member-only half over the real assembly.
- **The membership lifecycle clears the tenant that was left, and the guard
  keeps one tenant administrator.** `MembershipRemoved` withdraws every binding
  an account holds in the tenant it left, through the manager's own
  `ClearAccountBindings` path, on the ordinary path right after the removal
  commits; the `authn.membership.changed` subscription runs the same clear for
  the tenant its envelope names as the crash-window compensation. The cascade
  reaches the platform domain too: a platform-tenant removal takes the account's
  platform-domain bindings with it, because the platform administrator role
  exists only while the account is an active member of that tenant. A suspension
  clears nothing, because the membership can be resumed. The guard makes the
  business-tenant cascade safe: `CheckMembershipRemoval` refuses with
  `authn.ErrLastTenantAdmin` when the account is the named tenant's only
  `TenantAdminRole` binding, so a tenant that stays in service keeps someone who
  may govern it. `Lock` delegates to the product's `authz.TenantLock`,
  the same lock `DeleteBinding` takes: the host supplies that object and the
  policy module uses its in-process default when the host supplies none, so the
  guard's lock and the binding delete serialise together. `MembershipLock`
  answers that object for `authn.SharedMembershipLock`, so a removal holding
  several guards wired to one lock takes it once. Both reuse `tenantAdminRows` and
  `lastTenantAdminAfterRemoval`, the pair `DeleteBinding`'s own floor reads.
  `TestMembershipRemovedWithdrawsThePlatformBindings`,
  `TestCheckMembershipRemovalRefusesTheLastTenantAdmin`.
- **Both input items are configuration.** `authz.policy.load-timeout` bounds the
  startup load and is read before the snapshot exists.
  `authz.policy.refresh-interval` sizes the ticker `Start` raises and is read at
  construction; zero turns the periodic reload off, and a negative value is
  refused at startup. The module declares no runtime switch that loosens a
  judgement. `authz.policy.refresh-interval` was a setting before: a deployment
  upgrading from that version must copy a non-zero interval into the
  configuration, because the settings key is no longer declared — writing it
  through the settings store is refused, and the module does not read it.
- **The optional capabilities are taken up in `Init` and their absence is
  legal.** The roster, the bus and the logger are each optional: a policy module
  with no roster checks no platform-domain conflict, one with no bus relies on
  the configured refresh interval, and one with no logger writes through the
  process default. The database is the one hard dependency; the engine, the
  subject-permission read, the catalog, the manager, the seeder and the
  reconciler are delivered exclusively, so a second provider of any of them
  fails the startup. The seeder and the reconciler are taken up by the host's
  seeding step and by no management module.
- **A secret, a credential and a policy value reach no log line and no error
  text.** An account key and a tenant may be logged; a grant's rules and a
  binding's role reach no log. A refusal names the row that failed and never
  the value that failed it.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

CI runs the segments of `make check` as parallel jobs. On this package alone:

```
go -C pkg build ./authz/...
go -C pkg vet ./authz/policy/...
go -C pkg test ./authz/policy/...
go -C pkg test -race ./authz/policy/...
env -C pkg golangci-lint run ./authz/policy/...
```

The cases bring up a real database: SQLite through `pkg/internal/dbtest`, and
PostgreSQL through `pkg/internal/pgpool/pgtest`, which reaches the server the
repository's compose stack brings up; those cases carry the `integration` tag,
so a default run compiles none of them. The migration cases run the module's own
files; `policy_test.go` drives the management surface over a handle with the
tenant filter installed; `assembly_test.go` drives the policy and guard
descriptors through a real registry, which is where what each callback takes up
and what the two modules resolve from each other is observed; `module_test.go`
exercises the change event, the refresh interval and the load timeout. The
snapshot and the model self-check are tested under `pkg/authz/internal/policy`.
