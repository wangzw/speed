package tenant

import (
	"context"
	nethttp "net/http"
)

// Extractor reads a tenant name off the request. It returns false when the
// request carries none. It must not block and must not read the request body.
type Extractor interface {
	Extract(r *nethttp.Request) (string, bool)
}

// Resolution is the surface an extractor is registered on. Registration is
// accepted from this module's Migrate callback until its Start callback; a call
// outside that window panics.
type Resolution interface {
	// Register declares an extractor under a non-empty name free of blanks.
	// Two registrations under one name fail the startup.
	Register(name string, e Extractor)
}

// Admission is an ordering anchor for the layer that decides whether a caller
// may enter the tenant a request resolved to. It has no operations.
type Admission interface {
	// AdmissionLayer marks the implementation. It does nothing.
	AdmissionLayer()
}

// Catalog is the roster's read and write surface. None of its methods is scoped
// to a tenant, and all of them refuse while the roster has not been read.
//
// Methods that read or write the table run on a connection of their own, not in
// the caller's transaction, and fail with ErrNoConnection after five seconds
// without one. Get, ByName and List read the snapshot only.
type Catalog interface {
	// Get returns the tenant with this identifier. The second result is
	// false when the roster has no such row.
	Get(ctx context.Context, id ID) (Tenant, bool, error)
	// ByName returns the tenant this name belongs to.
	ByName(ctx context.Context, name string) (Tenant, bool, error)
	// Put registers a tenant, or rewrites the row its identifier already has.
	// It is idempotent by identifier and never changes a name or an owner that
	// is already written. It returns ErrInvalidTenant for a changed name, a
	// StatusDeleted value or a non-operator disable source, ErrNameConflict or
	// ErrTenantExists for a clash in the shared name and identifier namespace,
	// and ErrTenantDeleted for a tombstone.
	Put(ctx context.Context, t Tenant) error
	// Create registers a tenant whose identifier the table does not hold yet,
	// checked against the table rather than the snapshot. It returns
	// ErrTenantExists for a taken identifier, a tombstone included, and
	// refuses the same values as Put.
	Create(ctx context.Context, t Tenant) error
	// SetStatus is the operator's state transition. Disabling records the
	// operator as the source and enabling clears it. A StatusDeleted value is
	// refused with ErrInvalidTenant and a tombstone with ErrTenantDeleted.
	SetStatus(ctx context.Context, id ID, s Status) error
	// SetStatusByAccount is the state transition account state drives. It
	// disables only an active tenant and enables only a tenant the account
	// disabled; any other request writes nothing and returns nil. A tombstone
	// is refused with ErrTenantDeleted and StatusDeleted with ErrInvalidTenant.
	SetStatusByAccount(ctx context.Context, id ID, s Status) error
	// Delete writes one tenant as deleted, the terminal state. Deleting a
	// tombstone again returns nil. An unknown identifier is refused with
	// ErrInvalidTenant.
	Delete(ctx context.Context, id ID) error
	// SetOwner writes one tenant's owner account and nothing else. An empty or
	// malformed owner and an unknown identifier are refused with
	// ErrInvalidTenant, and a tombstone with ErrTenantDeleted.
	SetOwner(ctx context.Context, id ID, owner string) error
	// SetOwnerIfEmpty writes the owner only while the row carries none and
	// reports whether it wrote. It refuses what SetOwner refuses.
	SetOwnerIfEmpty(ctx context.Context, id ID, owner string) (bool, error)
	// SetProfile rewrites one tenant's profile and nothing else. A profile
	// outside its domain is refused with ErrInvalidProfile, an unknown
	// identifier with ErrInvalidTenant, and a tombstone with ErrTenantDeleted.
	SetProfile(ctx context.Context, id ID, profile Profile) error
	// List returns a copy of every tenant the roster holds, ordered by name.
	List(ctx context.Context) ([]Tenant, error)
	// Enter returns a context that belongs to this tenant, replacing any tenant
	// the one passed in carried. An unknown identifier is refused with
	// ErrInvalidTenant, a disabled tenant with ErrTenantDisabled and a
	// tombstone with ErrTenantDeleted; a refusal is read from the table, not the
	// snapshot. A table that cannot be read yields the read's own failure
	// (ErrRosterMissing, ErrNoConnection, a context error). On a refusal the
	// returned context carries no tenant.
	Enter(ctx context.Context, id ID) (context.Context, error)
	// EnterRegistered is Enter for a tenant whatever its status. It refuses
	// only an unknown identifier, with ErrInvalidTenant.
	EnterRegistered(ctx context.Context, id ID) (context.Context, error)
}
