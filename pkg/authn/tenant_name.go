package authn

import "context"

// TenantNameResolver answers the name a tenant identifier is addressed by.
//
// It carries the same answer as the tenant capability's NameResolver, declared
// here because no package of this directory imports the tenant directory. The
// roster module implements both, so one assembly delivers one answer through
// both names.
//
// The seam is optional where it is taken up: an assembly whose roster supplies
// none makes a view name no tenant, rather than falling back to the identifier,
// which is internal and must never reach a client.
type TenantNameResolver interface {
	// TenantNameOf answers the name of the tenant with this identifier. An
	// identifier no tenant carries answers the empty string and no error.
	TenantNameOf(ctx context.Context, id string) (string, error)
}

// TenantDirectory answers the identifier a tenant name names.
//
// It is the reverse answer TenantNameResolver gives, declared here for the
// request surfaces that receive a tenant as a name: an HTTP body or a query
// string names a tenant by its name, while the stores key their rows by the
// identifier. The roster module implements both, so one assembly delivers both
// directions over one snapshot.
//
// The seam is optional where it is taken up: an assembly whose roster supplies
// none cannot tell which tenant a name names, and a surface that would read a
// tenant from a request name refuses rather than treating the name as an
// identifier, which is internal and is never a value a request carries.
type TenantDirectory interface {
	// TenantIDOf answers the identifier of the tenant this name names. The
	// second result is false when no tenant carries the name, and the empty
	// identifier is never a tenant.
	TenantIDOf(ctx context.Context, name string) (string, bool, error)
}
