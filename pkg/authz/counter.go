package authz

import "context"

// TenantAdminCounter answers whether an account is the only tenant
// administrator of a business tenant that is still in service.
//
// The question outlives any one tenant, so it cannot come from Manager, whose
// every method names one tenant: an account may administer several, and the
// account-deletion path has to know whether any of them would lose its last
// administrator. The policy subpackage delivers the implementation, reading
// the policy table and the roster; a host supplies no second one.
//
// The seam is optional where the authentication capability takes it up. An
// assembly without it deletes accounts as it did before the seam existed,
// which is the documented behaviour of a host that names no policy module.
// An error is the answer being unavailable — the caller treats it as a refusal
// to proceed, never as "not sole".
//
// Only a business tenant in service counts. The implementation leaves out the
// platform domain, a tenant the roster does not carry or does not serve, and a
// tenant the host's WriteGuard refuses as personal: the floor this seam
// protects is a domain somebody still governs, not every row a binding names.
type TenantAdminCounter interface {
	// SoleTenantAdminTenant reports whether account holds the only
	// tenant-administrator binding of an active business tenant. The first
	// result names that tenant when the second is true, and is empty otherwise;
	// when several qualify, it is the first by identifier. An empty account
	// holds no binding, and the answer is not sole.
	//
	// A non-nil error means the policy table, the roster or the host's write
	// guard could not answer: the caller must treat it as unavailable and never
	// as permission.
	SoleTenantAdminTenant(ctx context.Context, account string) (tenant string, sole bool, err error)

	// LockAccountAdministration takes the tenant lock of every tenant in which
	// account holds a tenant-administrator binding, and answers one function
	// that releases them all. A deletion holds it from before
	// SoleTenantAdminTenant until its cleanup has removed the account's
	// bindings, so two deletions in one tenant cannot each read the other as
	// present and then together leave the tenant with no administrator. It is
	// the same lock Manager.DeleteBinding and MembershipGuard take, so a
	// deletion, a binding removal and a membership removal of one tenant cannot
	// interleave across the check.
	//
	// The tenants are locked in identifier order, so two deletions that
	// administer overlapping tenant sets cannot deadlock. The release is
	// idempotent. An error means the lock could not be taken and the deletion
	// must not proceed; it is never permission. An account with no
	// tenant-administrator binding locks nothing and answers a no-op release.
	//
	// One caveat travels with the implementation: the in-process default
	// serialises one process, and replicas that do not share a database are a
	// non-guarantee of the assembly (see authz.TenantLock).
	LockAccountAdministration(ctx context.Context, account string) (release func(), err error)
}

// TenantOwnerCounter answers whether an account owns a business tenant that is
// still in service.
//
// An organization that still has an owner may not have that account deleted or
// suspended: the organization would be left with nobody who can hand it on.
// The fact lives in the roster's owner column, so the answer is a seam from the
// capability that reads the roster, and the account lifecycle takes it up
// optionally beside TenantAdminCounter.
//
// Only a business tenant in service counts. The implementation leaves out the
// platform domain, a tenant the roster does not carry or does not serve, and a
// tenant the host's write guard refuses as personal: a personal organization
// follows its account and is ended with it, so it must not block the account's
// own deletion or suspension.
//
// An error means the roster or the host's write guard could not answer: the
// caller treats it as unavailable and never as permission.
type TenantOwnerCounter interface {
	// OwnsActiveBusinessTenant reports whether account owns an active business
	// tenant. The first result names that tenant when the second is true, and is
	// empty otherwise; when several qualify, it is the first by identifier. An
	// empty account owns nothing.
	OwnsActiveBusinessTenant(ctx context.Context, account string) (tenant string, owns bool, err error)
}
