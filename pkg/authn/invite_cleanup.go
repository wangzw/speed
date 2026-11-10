package authn

import "context"

// InvitationRevoker withdraws the pending invitations a tenant holds when the
// tenant is deleted. The invitation module delivers it exclusively, and the
// tenant ownership module takes it up to clean the invitations under the
// tombstone.
//
// The call is made after the deletion has committed and is best effort: a
// failure leaves the rows in place, and acceptance then refuses them because
// the tenant is no longer served. An implementation is idempotent and touches
// only pending rows; an accepted or already revoked invitation is not rewritten.
type InvitationRevoker interface {
	// RevokeTenantInvitations revokes every pending invitation of the tenant.
	// An empty tenant or a tenant with no pending invitation is nil.
	RevokeTenantInvitations(ctx context.Context, tenant string) error
}
