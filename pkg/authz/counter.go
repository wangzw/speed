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
}
