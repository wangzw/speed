package tenant

import "context"

// WriteGuard reports whether a tenant rejects an operator write. A host
// implements it. It acts on the operator's write paths alone;
// SetStatusByAccount does not consult it.
type WriteGuard interface {
	// Denied reports whether the tenant rejects the write op names. An error
	// means the answer could not be read and is never a pass.
	Denied(ctx context.Context, id ID, op WriteOp) (bool, error)
}

// WriteOp names an operator write a WriteGuard protects. The set is closed.
type WriteOp int

const (
	// WriteDisable is disabling a tenant through SetStatus. It is the zero value.
	WriteDisable WriteOp = iota
	// WriteDelete is deleting a tenant through Delete.
	WriteDelete
)

// Provisioner creates a tenant together with its first administrator. A host
// implements it.
type Provisioner interface {
	// Provision creates t and binds creator as its first tenant-admin. A failure
	// after the roster row is written leaves the row in place. Roster refusals
	// stay readable through errors.Is: ErrTenantExists and ErrNameConflict.
	Provision(ctx context.Context, t Tenant, creator string) error
}
