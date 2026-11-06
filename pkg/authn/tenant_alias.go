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
