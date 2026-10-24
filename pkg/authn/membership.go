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
