# pkg/tenant/ownership

The tenant capability's organization-lifecycle module: it delivers
`tenant.OrganizationWriter` and performs the owner transfer and the
organization deletion, the two writes that span the roster, the policy store
and the membership store.

## Boundaries

- **One transaction per write.** `TransferOwner` and `DeleteOrganization` each
  open one transaction on `db.Database.DB()` and hand the same `*gorm.DB` to
  every roster, policy and membership write. A step on a connection of its own
  would survive a rollback the other steps do not. Any failed step rolls the
  whole transaction back.
- **One lock per tenant.** Both methods take `authz.TenantLock` before the
  transaction and release it after the commit and the after-commit rereads.
  The lock is optional; without one the module uses its own in-process
  implementation, which serialises this module's transfers and deletions but
  not a policy binding delete or a membership removal. An assembly that wants
  those paths to exclude each other supplies the one object they all take.
- **The transfer writes the new owner first.** It refuses a target that is not
  an active member (`tenant.ErrOwnerNotMember`), grants `tenant-admin`, writes
  the roster owner, then clears the previous owner's bindings and removes its
  membership. A transfer to the current owner is a no-op. The target's
  membership is read inside the transaction under the account's row lock
  (`provisioning.MembershipWriter.MembershipTx`), so a concurrent suspension
  either is seen by the check or waits for the transfer to commit.
- **The deletion writes the tombstone and cleans the core relations.** It
  writes the roster tombstone, deletes the tenant's grants, bindings and roles,
  and deletes its memberships, all in that one transaction. It never writes
  another module's scoped table; those modules clean their data under the
  tombstone. The identifier and the alias stay on the tombstone and are never
  reusable.
- **`expectedOwner` is the optimistic check that makes a race one-sided.** The
  management surface passes the owner it observed for an owner-initiated
  deletion and the empty string for platform staff. A transfer that wins first
  leaves the deletion refused with `tenant.ErrOwnerChanged`; a deletion that
  wins first leaves the transfer refused with `tenant.ErrTenantDeleted`.
- **After-commit work is best effort.** The transaction-aware writes return the
  roster and policy snapshot rereads. The closures run once the commit has
  happened; a failure is a warning, never a failed write. The underlying write
  is already durable.
- **The module holds no table, no route and no configuration.** The roster owns
  the tenant row, `authz.policy` the roles, grants and bindings,
  `authn.account` the memberships. This module only joins their
  transaction-aware surfaces.

## Running it

From the repository root:

```
go -C pkg test ./tenant/ownership/...
go -C pkg test -race ./tenant/ownership/...
go -C pkg vet ./tenant/ownership/...
```

`integration_test.go` drives the real account, roster, policy and personal
modules over one SQLite database: the transfer hands the organization on, a
non-member is refused, a deletion cleans the core relations and leaves the
identifier and alias unusable, a failed step rolls the whole deletion back, a
transfer against the old owner's deletion leaves exactly one winner, an account
owning an active organization cannot be deleted or suspended, and the previous
owner can be deleted once the ownership has moved.
