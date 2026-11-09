package authz

import "context"

// TenantNameResolver answers the name a tenant identifier is addressed by.
//
// The authorization management module holds no tenant roster and imports no
// tenant package, so the roster delivers this seam through the authorization
// capability's own name. The roster implements it beside the tenant capability's
// NameResolver, so one assembly delivers one answer through both.
type TenantNameResolver interface {
	// TenantNameOf answers the name of the tenant with this identifier. An
	// identifier no tenant carries answers the empty string and no error.
	TenantNameOf(ctx context.Context, id string) (string, error)
}
