package tenant

import "context"

// NameResolver answers the name a tenant identifier is addressed by. It is
// optional; without one a view names no tenant.
type NameResolver interface {
	// NameOf answers the name of the tenant with this identifier. An unknown
	// identifier answers the empty string and no error.
	NameOf(ctx context.Context, id ID) (string, error)
}
