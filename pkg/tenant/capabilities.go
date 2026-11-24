package tenant

import (
	"context"
	nethttp "net/http"
)

// Extractor reads a tenant name off the request surface. It sees only the
// request, returns false when it finds no name, and must not block or read the
// request body.
type Extractor interface {
	Extract(r *nethttp.Request) (string, bool)
}

// Resolution is the capability the resolving subpackage delivers, and the
// surface an extractor is registered on. Registration is accepted from this
// module's Migrate callback until its Start callback; a call outside that window
// panics.
type Resolution interface {
	// Register declares an extractor under a name the configuration selects it
	// by. The name must be non-empty and free of blanks, and two registrations
	// under one name fail the startup.
	Register(name string, e Extractor)
}

// Admission is the capability the admission subpackage delivers. It is an
// ordering anchor: a layer that must sit inside admission names
// (*tenant.Admission)(nil) in its After and calls nothing on it.
type Admission interface {
	// AdmissionLayer marks the implementation. It does nothing.
	//
	// It is exported because a type of another package cannot implement an
	// interface whose method is unexported.
	AdmissionLayer()
}

// Catalog is the capability the roster subpackage delivers: the roster's read
// and write surface. None of its methods is scoped to a tenant, and none needs
// a tenant in its context. A roster that has not been read refuses these calls.
//
// Each method that reads or writes the table runs on a connection of its own,
// not in the caller's transaction, taken within five seconds; no connection
// fails with ErrNoConnection having done nothing. The statements run under the
// caller's context. Get, ByName and List read the snapshot only.
type Catalog interface {
	// Get returns the tenant with this identifier. The second result is
	// false when the roster has no such row.
	Get(ctx context.Context, id ID) (Tenant, bool, error)
	// ByName returns the tenant this name belongs to. The second result is
	// false when no tenant carries the name.
	ByName(ctx context.Context, name string) (Tenant, bool, error)
	// Put registers a tenant, or rewrites the row its identifier already has;
	// it is idempotent by identifier. The owner is written on an insert only,
	// and a rewrite naming another name is refused with ErrInvalidTenant. A
	// name held by another tenant, or equal to another tenant's identifier, is
	// refused with ErrNameConflict; an identifier equal to another tenant's name
	// with ErrTenantExists; a tombstone with ErrTenantDeleted. A value with
	// StatusDeleted or a disable source other than the operator's is refused
	// with ErrInvalidTenant.
	Put(ctx context.Context, t Tenant) error
	// Create registers a tenant that is not registered yet, checking the table
	// rather than the snapshot. An identifier the table already holds, a
	// tombstone's included, is refused with ErrTenantExists and nothing is
	// written. Name conflicts and invalid values are refused as Put refuses
	// them.
	Create(ctx context.Context, t Tenant) error
	// SetStatus is the operator's state transition. Disabling writes the
	// source as the operator's and enabling clears it. A StatusDeleted value is
	// refused with ErrInvalidTenant, and a tombstone with ErrTenantDeleted.
	SetStatus(ctx context.Context, id ID, s Status) error
	// SetStatusByAccount is the state transition account state drives. It
	// disables only an active tenant and enables only a tenant disabled by the
	// account; any other request writes nothing and returns nil. A tombstone is
	// refused with ErrTenantDeleted, and a StatusDeleted value with
	// ErrInvalidTenant.
	SetStatusByAccount(ctx context.Context, id ID, s Status) error
	// Delete writes one tenant as deleted, the terminal state, keeping the row
	// and recording the moment. Deleting a tombstone again writes nothing and
	// returns nil. An identifier no row carries, or one outside the domain, is
	// refused with ErrInvalidTenant.
	Delete(ctx context.Context, id ID) error
	// SetOwner writes one tenant's owner account and nothing else; it is for the
	// transaction of a confirmed transfer. An empty owner, or one carrying a
	// blank or control character, is refused with ErrInvalidTenant, as is an
	// identifier no row carries; a tombstone is refused with ErrTenantDeleted.
	// Writing the owner a row already carries is a no-op.
	SetOwner(ctx context.Context, id ID, owner string) error
	// SetOwnerIfEmpty writes one tenant's owner account only while the row
	// carries none, and reports whether it wrote. The owner and the identifier
	// are validated as SetOwner validates them.
	SetOwnerIfEmpty(ctx context.Context, id ID, owner string) (bool, error)
	// SetProfile rewrites one tenant's organization profile and nothing else. A
	// profile outside its domain is refused with ErrInvalidProfile, an
	// identifier no row carries with ErrInvalidTenant, and a tombstone with
	// ErrTenantDeleted.
	SetProfile(ctx context.Context, id ID, profile Profile) error
	// List returns every tenant the roster holds, ordered by name, as a copy.
	List(ctx context.Context) ([]Tenant, error)
	// Enter returns a context that belongs to this tenant, as a resolved
	// request's does, for a path with no request behind it. An identifier no
	// row carries is refused with ErrInvalidTenant, a disabled tenant with
	// ErrTenantDisabled, and a tombstone with ErrTenantDeleted. A refusal
	// from the snapshot is confirmed against the table; a table that cannot be
	// read gives the failure itself (ErrRosterMissing, ErrNoConnection, the
	// context's error, or the read's own error). An admission comes from the
	// snapshot, so a tenant disabled on another replica is still entered until
	// this replica rereads the roster.
	//
	// The returned context carries no cross-tenant mark and replaces any tenant
	// the one passed in carried; on a refusal it carries neither a tenant nor the
	// mark.
	Enter(ctx context.Context, id ID) (context.Context, error)
	// EnterRegistered is Enter for a tenant whatever its status: it refuses only
	// an identifier no row carries, with ErrInvalidTenant.
	EnterRegistered(ctx context.Context, id ID) (context.Context, error)
}
