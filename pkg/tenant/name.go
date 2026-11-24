package tenant

import "context"

// NameResolver answers the name a tenant identifier is addressed by. It is
// optional: without one, a view names no tenant rather than falling back to the
// identifier.
type NameResolver interface {
	// NameOf answers the name of the tenant with this identifier. An identifier no
	// tenant carries answers the empty string and no error; a roster that has not
	// been read is an error.
	NameOf(ctx context.Context, id ID) (string, error)
}
