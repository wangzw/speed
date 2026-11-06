package authn

import "context"

// TenantAliasResolver answers the alias a tenant identifier is addressed by.
//
// It carries the same answer as the tenant capability's AliasResolver, declared
// here because no package of this directory imports the tenant directory. The
// roster module implements both, so one assembly delivers one answer through
// both names.
//
// The seam is optional where it is taken up: an assembly whose roster supplies
// none makes a view name no tenant, rather than falling back to the identifier,
// which is internal and must never reach a client.
type TenantAliasResolver interface {
	// TenantAliasOf answers the alias of the tenant with this identifier. An
	// identifier no tenant carries answers the empty string and no error.
	TenantAliasOf(ctx context.Context, id string) (string, error)
}

// TenantDirectory answers the identifier a tenant alias names.
//
// It is the reverse answer TenantAliasResolver gives, declared here for the
// request surfaces that receive a tenant as a name: an HTTP body or a query
// string names a tenant by its alias, while the stores key their rows by the
// identifier. The roster module implements both, so one assembly delivers both
// directions over one snapshot.
//
// The seam is optional where it is taken up: an assembly whose roster supplies
// none cannot tell which tenant an alias names, and a surface that would read a
// tenant from a request name refuses rather than treating the name as an
// identifier, which is internal and is never a value a request carries.
type TenantDirectory interface {
	// TenantIDOf answers the identifier of the tenant this alias names. The
	// second result is false when no tenant carries the alias, and the empty
	// identifier is never a tenant.
	TenantIDOf(ctx context.Context, alias string) (string, bool, error)
}
