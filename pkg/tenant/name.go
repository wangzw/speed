package tenant

import "context"

// NameResolver answers the name a tenant identifier is addressed by; the
// identifier is internal and never reaches a client. The seam is optional: without
// one a view names no tenant rather than falling back to the identifier.
type NameResolver interface {
	// NameOf answers the name of the tenant with this identifier. An identifier
	// no tenant carries answers the empty string and no error; a roster that has
	// not been read is a failure of the call.
	NameOf(ctx context.Context, id ID) (string, error)
}
