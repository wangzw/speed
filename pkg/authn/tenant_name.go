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

// TenantState is how a tenant stands towards being served, as the
// authentication directory can read it. It is the roster's status reduced to
// what an authentication path has to judge: a tenant is served, or it is
// disabled and may be served again, or it is deleted and never will be.
type TenantState uint8

const (
	// TenantServing is a tenant whose requests are served.
	TenantServing TenantState = iota
	// TenantDisabled is a tenant that exists but is not served. Its data stays
	// and enabling it makes it served again.
	TenantDisabled
	// TenantDeleted is a tenant that has been deleted. It is terminal.
	TenantDeleted
)

// TenantStateResolver answers how a tenant stands, for authentication paths
// that must not admit a tenant which is not served. It carries the same facts
// as the tenant capability's status, declared here because no package of this
// directory imports the tenant directory.
//
// The seam is optional where it is taken up: an assembly whose roster supplies
// none has no tenant state to consult. An identifier no tenant carries answers
// TenantDeleted: a tenant that is not there is not served.
type TenantStateResolver interface {
	// TenantStateOf answers the state of the tenant with this identifier.
	TenantStateOf(ctx context.Context, id string) (TenantState, error)
}

// TenantDescription is what an authentication path needs to name a tenant to a
// visitor and to judge whether it is an organization at all.
type TenantDescription struct {
	// Name is the name the tenant is addressed by. The empty string names no
	// tenant.
	Name string
	// DisplayName is the name the organization is shown by, free text its
	// administrators chose. It is empty for a tenant that carries none.
	DisplayName string
	// System reports whether this is the system tenant, the platform's own
	// domain rather than an organization. Nothing can be invited into it.
	System bool
}

// TenantDescriptionResolver answers what a tenant is called and whether it is
// the system tenant, in one read. It carries the facts the tenant capability
// holds, declared here because no package of this directory imports the tenant
// directory.
//
// The seam is required by the invitation capability: it refuses the system
// tenant and names an organization in every public link, neither of which a
// view can leave out, so an assembly without it fails the startup. An
// identifier no tenant carries answers the zero description with no error.
type TenantDescriptionResolver interface {
	// TenantDescriptionOf answers the description of the tenant with this
	// identifier.
	TenantDescriptionOf(ctx context.Context, id string) (TenantDescription, error)
}
