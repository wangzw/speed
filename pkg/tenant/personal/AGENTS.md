# pkg/tenant/personal

The tenant capability's personal-tenant module: it implements
`provisioning.AccountProvisioning`, `authn.AccountLifecycle` and
`tenant.KindResolver`.

## Boundaries

- **The account's transaction is the boundary.** `ProvisionAccount` and
  `ProvisionAccountDeletion` write through the `*gorm.DB` the account module
  hands in and open no connection of their own. A write on another connection
  would survive a rollback the account does not, or leave a committed account
  without the space it owns. The two methods return an error after any failed
  step; the account module rolls its own rows back with it.
- **The module writes five pieces, in this order: the tenant row, the creator's
  membership, the `tenant-admin` role, its grants and the creator's binding.**
  Every transaction-aware write returns after-commit work, and every closure is
  handed to `provisioning.AfterCommitOf(ctx)`. The account module runs them once
  its transaction has committed.
- **A service account owns no organization.** Both provisioning methods return
  nil for `kind=service` and write nothing.
- **The name is the username.** The personal tenant is addressed by the
  account's username; its identifier remains `personal-<accountID>`. A user
  account always has a username: SSO JIT obtains one through `usernameStrategy`
  (a claim or user confirmation), a service account owns no personal tenant,
  and the write surface produces no username-less user account. The seam refuses
  an empty username with `ErrUsernameMissing`; no derived name exists.
- **An existing tenant row is a replay, not a failure.**
  `tenant.ErrTenantExists` continues with the remaining steps.
  `tenant.ErrNameConflict` becomes this module's exported `ErrNameConflict`,
  which wraps the roster's sentinel; it aborts the account's transaction.
- **The lifecycle hooks are the retry path, not the authority.** After the
  account's transaction commits, `AccountCreated` completes anything the seam
  left unfinished, the status hooks move the tenant with the account, and
  `AccountDeleted` winds it down. Every hook is idempotent. The in-transaction
  deletion clears the deleted account's bindings, removes the tenant-admin role
  and writes the tombstone; the deletion hook then removes the tenant's other
  memberships, including when the tombstone is already there, because the
  member listing has no transaction-aware form. A hook that fails leaves the
  earlier steps for the next call.
- **The event compensation subscribes the same hooks.** With an event bus in the
  assembly, `authn.account.status_changed` and `authn.account.deleted` rerun the
  matching hook. The status handler reads the account's current state first, so
  a late event cannot apply a state the account has left. Events are
  de-duplicated in a bounded in-memory set.
- **The module is always in force when assembled.** There is no configuration
  item. It declares the static feature `org.personal` and mounts no HTTP route:
  the account's transaction is the guarantee that the personal tenant exists.
- **The module provides `tenant.KindResolver` rather than requiring one.** It is
  the one place that knows which identifiers are personal. `tenant.self` takes
  it optionally; an assembly without this module reads every tenant as
  `business`.
- **The module holds no table and no migration.** The tenant row belongs to
  `tenant.catalog`, the membership to `authn.account`, the role, grants and
  binding to `authz.policy`, and the audit rows to `audit`.

## Running it

From the repository root:

```
go -C pkg test ./tenant/personal/...
go -C pkg test -race ./tenant/personal/...
go -C pkg vet ./tenant/personal/...
```

`provisioning_test.go` pins the five transaction-aware writes, the service-account
no-op, the replay, the empty-username refusal, the name sentinel and the
after-commit collector. `lifecycle_test.go` pins the create, disable, enable and
deletion hooks, their order and their idempotence. `ensure_test.go` pins the
post-commit completion, the username refusal and the kind resolver.
`events_test.go` pins the compensation. `integration_test.go` drives
the real account, roster and policy modules over one SQLite database and pins
the shared transaction: a failure after the tenant row is written leaves no
account, a name conflict leaves no account, a failed tombstone leaves the
account in service, a committed creation leaves its audit row, and a deletion
removes the other members of the personal tenant. `module_test.go` pins the
descriptor.
