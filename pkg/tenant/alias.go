package tenant

import "context"

// AliasResolver answers the alias a tenant identifier is addressed by.
//
// A tenant's identifier is internal: it is what the roster and the scoped
// tables key rows by, and it never reaches a client. The alias is the name the
// request surface addresses the tenant by, and every view that names a tenant
// answers the alias.
//
// The seam is optional where it is taken up. An assembly whose roster delivers
// no implementation supplies none, and a view then names no tenant rather than
// falling back to the identifier: the identifier is not a name a client may
// read, so a missing seam must not leak one.
//
// It is implemented by the roster module, which is the thing that holds the
// identifier-to-alias mapping, and taken up by the modules whose views or audit
// rows name a tenant. The authentication directory does not import this package,
// so its modules take up the same data through the authentication capability's
// own interface; the roster implements both.
type AliasResolver interface {
	// AliasOf answers the alias of the tenant with this identifier. An
	// identifier no tenant carries answers the empty string and no error: it is
	// a fact about the row, not a failure. A roster that has not been read is a
	// failure of the call.
	AliasOf(ctx context.Context, id ID) (string, error)
}
