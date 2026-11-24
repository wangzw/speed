package authz

import "context"

// TenantNameResolver answers the name a tenant identifier is addressed by.
type TenantNameResolver interface {
	// TenantNameOf answers the name of the tenant with this identifier. An
	// identifier no tenant carries answers the empty string and no error.
	TenantNameOf(ctx context.Context, id string) (string, error)
}
