package authn

import "context"

// Memberships reads and writes which tenants an account belongs to. The account
// module delivers it exclusively; the tenant admission layer, the login engine,
// the impersonation module, the administration routes and a permission model
// take it by this interface, and none of them reads the table.
//
// The tenant is the string form of a tenant identifier, and this interface knows
// no tenant type. A membership that cannot be found and one that is suspended are
// both read as no membership, because a Membership's zero value is suspended. A
// subject of the static credential table has no membership row, and IsMember is
// false for it.
//
// Reading one membership is Membership and writing one is Put, so that the names
// of this interface and of Accounts and Passwords, which the account module
// delivers beside it, are disjoint.
type Memberships interface {
	// IsMember reports whether the account is an active member of the tenant.
	IsMember(ctx context.Context, id AccountID, tenant string) (bool, error)
	// Membership reads one membership. The second result is false when there is
	// none.
	Membership(ctx context.Context, tenant string, id AccountID) (Membership, bool, error)
	// Tenants lists every membership of an account across tenants, ordered by
	// tenant identifier ascending.
	Tenants(ctx context.Context, id AccountID) ([]Membership, error)
	// Members lists the memberships of a tenant, one page at a time.
	Members(ctx context.Context, tenant string, page Page) ([]Membership, error)
	// Put writes a membership, idempotently: adding, suspending and resuming are
	// one call with the Membership's values. An account that does not exist, a
	// deleted one included, is ErrAccountNotFound, because the tables have no
	// foreign key and this method is what checks.
	Put(ctx context.Context, m Membership) error
	// Remove deletes one membership.
	Remove(ctx context.Context, id AccountID, tenant string) error
}

// MembershipLifecycle is the hook a module that holds data keyed by a membership
// implements, so that the data can be cleaned after an explicit membership
// removal commits. The policy module is the implementation the authentication
// capability ships beside: an account that leaves the platform tenant must lose
// its platform-domain bindings at that point, not only when the membership event
// is eventually delivered. The rule for who implements it is holding data keyed
// by a membership, so a host's own module joins by implementing it; the
// implementations stand side by side, none is exclusive, and the account module
// takes all of them with core.ResolveAll in its Init and does not list them in
// Requires.
//
// The hook is called after the removal's transaction has committed and not in
// it. It is idempotent: an account already holding nothing in the tenant is nil.
// A hook that fails makes the call return ErrCleanupIncomplete with the removal
// in force, and repeating the same call finishes the cleanup. The
// membership.changed event stays as the compensation for a crash between the
// commit and the hook.
type MembershipLifecycle interface {
	// MembershipRemoved withdraws what the module holds for the account in the
	// tenant. The account's membership row is already gone, so an implementation
	// reads no membership and must not depend on one.
	MembershipRemoved(ctx context.Context, id AccountID, tenant string) error
}

// MembershipCleanup removes a membership on the account-deletion path. The
// account module delivers it beside Memberships.
//
// The removal through Memberships asks every MembershipGuard, which keeps a
// domain that stays in service governable or a space that must stay single
// member. Account deletion is the account's terminal winding-up, and the
// deletions its own cleanup collects are the rows an administrator added, so it
// removes through this interface instead: no guard is asked, and no
// MembershipLifecycle hook runs, because the deletion's own cleanup is what
// withdraws the data the modules hold. Each removed row still publishes
// authn.membership.changed, so a subscriber that cleans on that event keeps
// working on this path too. An implementation is idempotent: a row that is not
// there is already removed.
type MembershipCleanup interface {
	// RemoveMembershipForDeletion deletes one membership without asking the
	// guards or calling the membership lifecycle hooks; the membership.changed
	// event is still published for the row.
	RemoveMembershipForDeletion(ctx context.Context, id AccountID, tenant string) error
}

// MembershipGuard is asked before an explicit membership removal, so that a
// module which holds a tenant's policy can refuse a removal that would leave the
// tenant with nobody who may govern it, or leave a space that must stay single
// member. The policy module implements the last-administrator floor, and a host
// that declares policy of its own adds a guard beside it — the demo host refuses
// every removal in a personal tenant.
//
// The implementations stand side by side: the account module takes every one
// with core.ResolveAll, each is asked, and any refusal refuses the removal. The
// seam is optional. An assembly with no guard removes memberships as it did
// before the seam existed, and an answer that cannot be read is a refusal to
// proceed rather than a pass. Account deletion does not consult the guards: the
// rule keeps a domain that stays in service governable, while a deleted
// account's erasure is its own terminal winding-up and clears its bindings on
// that path.
type MembershipGuard interface {
	// Lock takes the tenant's removal lock and answers the function that
	// releases it. The removal route takes every guard's lock before it asks
	// any guard's check, holds them across the checks and the removal's commit,
	// and releases them after. An implementation that holds no data a
	// concurrent removal could race may take no lock and answer a release that
	// does nothing.
	//
	// The lock is per tenant and process-wide or wider: an in-process lock
	// serialises the removals this process serves, and a lock the shared
	// database carries serialises the replicas that share it too. Nothing
	// serialises replicas that do not share a database, which is a
	// non-guarantee of the assembly rather than of this seam.
	//
	// An error means the lock could not be taken and the removal must not
	// proceed: it is neither a refusal nor a pass. A release may be called more
	// than once — the removal releases it after the commit and again on its
	// error path — so an implementation must make it idempotent.
	Lock(ctx context.Context, tenant string) (release func(), err error)

	// CheckMembershipRemoval reports ErrLastTenantAdmin when removing the
	// account's membership of the tenant would leave the tenant with no
	// tenant-administrator binding, and nil when it would not. Any other error
	// is the check being unavailable.
	CheckMembershipRemoval(ctx context.Context, id AccountID, tenant string) error
}
