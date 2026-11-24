package tenant

import (
	"context"
	nethttp "net/http"
)

// Extractor reads a tenant name off the request surface; the roster turns it into an identifier,
// and an extractor does not know the roster exists. A well-formed name that names no tenant is the
// roster's to refuse. It runs on every guarded request and must not block or read the body.
type Extractor interface {
	Extract(r *nethttp.Request) (string, bool)
}

// Resolution is the capability the resolving subpackage delivers and the surface an extractor is
// registered on. Registration is accepted from Migrate through Start; a call outside that window
// panics, because a registering module declares no dependency on this one.
type Resolution interface {
	// Register declares an extractor under a name the configuration selects it
	// by. The name is non-empty and free of blanks; two registrations under one
	// name fail the startup, naming the name and both implementation types.
	Register(name string, e Extractor)
}

// Admission is the capability the admission subpackage delivers: the layer that decides whether an
// identified caller may enter the tenant a request resolved to. It is an ordering anchor, not a
// surface: a layer inside it names (*tenant.Admission)(nil) in its After and calls nothing.
type Admission interface {
	// AdmissionLayer marks the implementation. It does nothing.
	AdmissionLayer()
}

// Catalog is the capability the roster subpackage delivers: the roster's read and write surface, taken
// up by a host to register tenants and change their state. The table-writing methods use their own
// pooled connection and fail with ErrNoConnection; Get, ByName and List read the snapshot.
type Catalog interface {
	// Get returns the tenant with this identifier. The second result is
	// false when the roster has no such row.
	Get(ctx context.Context, id ID) (Tenant, bool, error)
	// ByName returns the tenant this name belongs to. It is the only lookup
	// the resolving layer makes on every request: the identifier is internal
	// and is never a name a request surface carries.
	ByName(ctx context.Context, name string) (Tenant, bool, error)
	// Put registers a tenant or rewrites the row its identifier already has, taking the status and
	// disable source but not the name or owner. A tombstone is ErrTenantDeleted, a name conflict
	// ErrNameConflict, a cross-namespace insert ErrTenantExists, a deleted status ErrInvalidTenant.
	Put(ctx context.Context, t Tenant) error
	// Create registers a tenant that is not registered yet and refuses an identifier the table already
	// holds with ErrTenantExists, writing nothing. The value's owner, profile and creation moment are
	// written with the row; name conflicts and a deleted status are refused as Put refuses them.
	Create(ctx context.Context, t Tenant) error
	// SetStatus is the operator's transition between active and disabled. Disabling writes the
	// operator source even on an already-disabled row, and enabling clears it. StatusDeleted is
	// ErrInvalidTenant and a tombstone ErrTenantDeleted.
	SetStatus(ctx context.Context, id ID, s Status) error
	// SetStatusByAccount is the transition account state drives: it disables only an active tenant
	// with the account source, and enables only a tenant the account disabled. Any other request
	// writes nothing; a tombstone is ErrTenantDeleted and StatusDeleted ErrInvalidTenant.
	SetStatusByAccount(ctx context.Context, id ID, s Status) error
	// Delete writes one tenant as deleted, the terminal state, recording the moment and leaving the
	// row, identifier, name, owner and disable source in place. The tenant is never served again, a
	// second delete is a no-op, and an unknown or out-of-domain identifier is ErrInvalidTenant.
	Delete(ctx context.Context, id ID) error
	// SetOwner writes one tenant's owner account and nothing else, for the transaction of a confirmed
	// transfer. An empty, blank or control-character owner is ErrInvalidTenant, as is an unknown
	// identifier; writing the value a row already carries is a no-op, and a tombstone is ErrTenantDeleted.
	SetOwner(ctx context.Context, id ID, owner string) error
	// SetOwnerIfEmpty writes one tenant's owner account only while the row carries none, and reports
	// whether it wrote; it repairs a row that predates the owner rule. The condition is in the
	// statement. An unknown identifier is ErrInvalidTenant and a tombstone ErrTenantDeleted.
	SetOwnerIfEmpty(ctx context.Context, id ID, owner string) (bool, error)
	// SetProfile rewrites one tenant's organization profile and nothing else: display name,
	// description, contact email and website. A profile outside this package's domain is
	// ErrInvalidProfile; an unknown identifier is ErrInvalidTenant and a tombstone ErrTenantDeleted.
	SetProfile(ctx context.Context, id ID, profile Profile) error
	// List returns every tenant the roster holds, ordered by name, as a copy:
	// a caller sorting or trimming what it was handed does not disturb what
	// the next caller sees.
	List(ctx context.Context) ([]Tenant, error)
	// Enter returns a context that belongs to this tenant: FromContext answers it, scoped statements under
	// it are conditioned, filled and checked on it, and the logger carries the identifier. An unknown
	// identifier is ErrInvalidTenant, disabled ErrTenantDisabled, and a tombstone ErrTenantDeleted.
	Enter(ctx context.Context, id ID) (context.Context, error)
	// EnterRegistered is Enter for a tenant whatever its status: it enters a disabled tenant as well
	// as a served one, and refuses only an unknown identifier, with ErrInvalidTenant. It is how work
	// that belongs to a tenant is done while that tenant is not served.
	EnterRegistered(ctx context.Context, id ID) (context.Context, error)
}
