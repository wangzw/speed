# pkg/tenant/ownership

The tenant capability's organization-lifecycle module: it delivers
`tenant.OrganizationWriter` and `tenant.OwnershipTarget`, and performs the
owner-transfer request, its confirmation, decline and cancellation, and the
organization deletion — the writes that span the roster, the policy store and
the membership store. It holds exactly one table, `tenant_owner_transfers`, the
pending and resolved requests.

## Boundaries

- **One transaction per write.** The confirmation, the request and the deletion
  each open one transaction on `db.Database.DB()` and hand the same `*gorm.DB` to
  every roster, policy, membership and transfer-row write. A step on a connection
  of its own would survive a rollback the other steps do not. Any failed step
  rolls the whole transaction back. Cancellation and decline are single
  conditional updates; the condition is the arbitration, and a row another writer
  already resolved matches nothing.
- **One lock per tenant.** The request, confirmation, decline, cancellation and
  deletion take `authz.TenantLock` before the transaction and release it after the
  commit and the after-commit rereads. The lock is optional; without one the
  module uses its own in-process implementation, which serialises this module's
  writes but not a policy binding delete or a membership removal. An assembly
  that wants those paths to exclude each other supplies the one object they all
  take.
- **The request changes no ownership.** `RequestTransfer` refuses a target that
  is not an active member (`tenant.ErrOwnerNotMember`), a target whose account is
  not active (`tenant.ErrOwnerInactive`) after reading it through
  `authn.Accounts` under the tenant lock, a tenant with no owner
  (`tenant.ErrNoOwner`), a target that is already the owner
  (`tenant.ErrTransferTargetIsOwner`) and a second pending request
  (`tenant.ErrTransferAlreadyPending`; the partial unique index is the arbiter).
  The roster is read before the membership, so a missing tenant is
  `tenant.ErrInvalidTenant` and a tombstone `tenant.ErrTenantDeleted`, never
  `tenant.ErrOwnerNotMember`. A non-empty `expectedOwner` that no longer matches
  the roster row is `tenant.ErrOwnerChanged`. The request row records the
  roster's owner at request time as `from`, not the account that pressed the
  button, so the confirmation can check that the requester still owns the
  organization.
- **Only confirmation writes the owner.** `ConfirmTransfer` re-reads the request
  under the tenant lock, refuses a terminal or expired one, checks the target's
  account and membership again and checks that `from` still owns the roster row,
  then grants `tenant-admin`, writes the owner column and marks the request
  confirmed — all in one transaction. The previous owner stays an active member
  and keeps every binding it held, `tenant-admin` included. The target's
  membership is read inside the transaction under the account's row lock
  (`provisioning.MembershipWriter.MembershipTx`), so a concurrent suspension
  either is seen by the check or waits for the confirmation to commit.
- **Expiry is derived, never scheduled.** Each read and action first settles rows
  whose `expires_at` has passed with one conditional update, so a request never
  answers as pending past its window and no background job is needed. The
  conditional update needs no tenant lock: it changes no ownership and
  serialises in the database with the confirmation's own conditional update.
- **Deletion cancels the pending request in the same transaction.** The
  cancellation shares the tombstone's transaction, so a failure on either side
  rolls both back.
- **The target acts on its own identity.** The target-side methods take the
  caller's account and nothing else: no tenant context, no organization
  permission. A request that does not exist and one addressed to another account
  are refused with distinct sentinels that a route answers alike, revealing no
  existence.
- **The pending table carries no tenant model, and every store method is
  predicated.** The table is organization-lifecycle metadata, like the roster row
  itself, and the target's confirmation is an account-level request with no
  tenant context, so it cannot depend on one. The store therefore exposes no
  method that lists every row: each read and write carries an explicit
  `tenant_id` or `to_account` predicate.
- **Notifications are best effort.** A request notifies the target and a
  confirmation, decline or expiry notifies the requester through the optional
  `notification.Dispatcher`; a missing dispatcher, an unenterable tenant or a
  failed dispatch is a warning and never a failed write. The four notification
  types are declared in this module's `Resources`, with their text in its own
  locale catalogs, and go out in-app only: a target need not have a proved email
  address, and the target page is the authoritative entry either way.
- **The deletion writes the tombstone and cleans the core relations.** It
  writes the roster tombstone, deletes the tenant's grants, bindings and roles,
  deletes its memberships and cancels its pending transfer requests, all in that
  one transaction. It never writes another module's scoped table; those modules
  clean their data under the tombstone. The identifier and the name stay on the
  tombstone and are never reusable. The optional `authn.InvitationRevoker` is
  asked once the deletion has committed to withdraw the tenant's pending
  invitations; the invitation module owns those rows, and a failure is a warning
  rather than a failed deletion.
- **`expectedOwner` is the optimistic check that makes a race one-sided.** The
  request, cancellation and deletion take it: the management surface passes the
  owner it observed for an owner-initiated action, and the empty string for
  platform staff. A confirmation that wins first leaves the old owner's deletion
  refused with `tenant.ErrOwnerChanged`, and an earlier transfer leaves a request
  or cancellation from the stale read refused the same way; a deletion that wins
  first leaves a request or confirmation refused with `tenant.ErrTenantDeleted`
  or `tenant.ErrTransferNotPending`.
- **After-commit work is best effort.** The transaction-aware writes return the
  roster and policy snapshot rereads. The closures run once the commit has
  happened; a failure is a warning, never a failed write. The underlying write
  is already durable.
- **The module holds one table, no route and no configuration.** The roster owns
  the tenant row, `authz.policy` the roles, grants and bindings,
  `authn.account` the memberships, and this module owns
  `tenant_owner_transfers`. It joins the other stores' transaction-aware
  surfaces and mounts nothing: the HTTP face lives in `tenant.admin`.

## Running it

From the repository root:

```
go -C pkg test ./tenant/ownership/...
go -C pkg test -race ./tenant/ownership/...
go -C pkg vet ./tenant/ownership/...
```

`integration_test.go` drives the real account, roster, policy and personal
modules over one SQLite database: the transfer hands the organization on while
the previous owner stays an active member holding every binding it had, a
non-member, an inactive account and a deleted account are refused, a transfer
from an owner the row no longer carries is refused with `ErrOwnerChanged`, a
missing tenant and a tombstone are reported before the membership, a deletion
cleans the core relations including a seeded grant and leaves the identifier and
name unusable, a failed step rolls the whole deletion back, a confirmation
against the old owner's deletion leaves exactly one winner, an account owning an
active organization cannot be deleted or suspended, the account-administration
lock covers a member-only account so a request to it blocks, and the previous
owner can be deleted once the ownership has moved.

`request_test.go` holds the confirmation lifecycle: a request changes no
ownership, a second request is refused, only the target acts, confirming lands
the transfer and keeps the previous owner's member row and binding, declining and
cancelling leave the owner unchanged, an expired request cannot be confirmed, a
target that left or was suspended is refused, deletion cancels the pending row,
and the target receives the notice.
