package authn

import "context"

// Memberships reads and writes which tenants an account belongs to, and the
// account module delivers it exclusively. A missing membership and a suspended one
// both read as no membership; a static credential subject has no membership.
type Memberships interface {
	// IsMember reports whether the account is an active member of the tenant.
	IsMember(ctx context.Context, id AccountID, tenant string) (bool, error)
	// Membership reads one membership. The second result is false when there is
	// none.
	Membership(ctx context.Context, tenant string, id AccountID) (Membership, bool, error)
	// Tenants lists every membership of an account across tenants, ordered by
	// tenant identifier ascending.
	Tenants(ctx context.Context, id AccountID) ([]Membership, error)
	// Members lists the memberships of a tenant, one page at a time. A query on
	// the page narrows the listing to the accounts whose display name or login
	// identifiers begin with it; an empty query is no filter.
	Members(ctx context.Context, tenant string, query MemberQuery) ([]Membership, error)
	// Put writes a membership idempotently: adding, suspending and resuming are one
	// call with the Membership's values. An account that does not exist is
	// ErrAccountNotFound.
	Put(ctx context.Context, m Membership) error
	// PutJoining writes a membership for an account joining a tenant: the account
	// ends active only if it had no membership or its membership was still active,
	// and the second result reports whether it is an active member after the call.
	PutJoining(ctx context.Context, m Membership, wasActive bool) (joined bool, err error)
	// Remove deletes one membership.
	Remove(ctx context.Context, id AccountID, tenant string) error
}

// MembershipLifecycle is the hook a module holding data keyed by a membership
// implements so that data is cleaned after an explicit removal commits. Every
// implementation is called after the commit, idempotently; a failure is ErrCleanupIncomplete.
type MembershipLifecycle interface {
	// MembershipRemoved withdraws what the module holds for the account in the
	// tenant. The account's membership row is already gone, so an implementation
	// reads no membership and must not depend on one.
	MembershipRemoved(ctx context.Context, id AccountID, tenant string) error
}

// MemberRoleReader is the optional bulk read a module holding role bindings
// implements, mapping each account of a tenant to its sorted, duplicate-free role
// names; an account with no binding is absent or empty. An empty tenant is ErrInvalidArgument.
type MemberRoleReader interface {
	// MemberRoles answers the role names every account holds in the tenant,
	// keyed by account. The tenant is the string form of a tenant identifier as
	// everywhere else in this package, and an empty one is ErrInvalidArgument.
	MemberRoles(ctx context.Context, tenant string) (map[AccountID][]string, error)
}

// MembershipCleanup removes a membership on the account-deletion path: no guard
// is asked and no MembershipLifecycle hook runs, but the membership.changed event
// is still published. It is idempotent, and the account module delivers it.
type MembershipCleanup interface {
	// RemoveMembershipForDeletion deletes one membership without asking the
	// guards or calling the membership lifecycle hooks; the membership.changed
	// event is still published for the row.
	RemoveMembershipForDeletion(ctx context.Context, id AccountID, tenant string) error
}

// MembershipGuard is asked before an explicit membership removal and can refuse
// one that would leave a tenant ungovernable or a single-member space empty. Every
// implementation is asked and any refusal refuses; account deletion skips guards.
type MembershipGuard interface {
	// Lock takes the tenant's removal lock and returns its release. The removal
	// takes every guard's lock before any check and holds them through the commit;
	// an error stops the removal, and a release may be called more than once.
	Lock(ctx context.Context, tenant string) (release func(), err error)

	// CheckMembershipRemoval reports ErrLastTenantAdmin when removing the account's
	// membership would leave the tenant with no tenant-administrator binding, and nil
	// otherwise. Any other error is the check being unavailable.
	CheckMembershipRemoval(ctx context.Context, id AccountID, tenant string) error
}

// SharedMembershipLock is implemented by a guard whose Lock takes a lock object
// another guard takes too; the removal path takes each distinct object once and
// the value must be comparable.
type SharedMembershipLock interface {
	// MembershipLock returns the shared lock object, or nil when the lock is the
	// guard's own.
	MembershipLock() any
}
