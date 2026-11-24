package authz

import "context"

// TenantAdminCounter answers whether an account is the only tenant
// administrator of a business tenant that is still in service.
//
// Only a business tenant in service counts: the platform domain, a tenant the
// roster does not carry or serve, and a tenant the host's WriteGuard refuses as
// personal are left out. An error means the answer is unavailable, never "not
// sole".
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
	// that releases them all. It is the same lock Manager.DeleteBinding and
	// MembershipGuard take. Tenants are locked in identifier order, so
	// overlapping deletions cannot deadlock, and the release is idempotent. An
	// error means the lock could not be taken and the deletion must not proceed.
	// An account with no tenant-administrator binding answers a no-op release.
	// The in-process default serialises one process only; see authz.TenantLock.
	LockAccountAdministration(ctx context.Context, account string) (release func(), err error)
}

// TenantOwnerCounter answers whether an account owns a business tenant that is
// still in service.
//
// Only a business tenant in service counts: a personal tenant follows its
// account and must not block the account's deletion or suspension. An error
// means the roster or the host's write guard could not answer; the caller
// treats it as unavailable and never as permission.
type TenantOwnerCounter interface {
	// OwnsActiveBusinessTenant reports whether account owns an active business
	// tenant. The first result names that tenant when the second is true, and is
	// empty otherwise; when several qualify, it is the first by identifier. An
	// empty account owns nothing.
	OwnsActiveBusinessTenant(ctx context.Context, account string) (tenant string, owns bool, err error)
}
