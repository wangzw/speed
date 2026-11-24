package tenant

import "context"

// WriteGuard reports whether a tenant rejects an operator write. It acts on the
// operator's write paths only, not on SetStatusByAccount; an error from it is a
// refusal to proceed, never permission.
type WriteGuard interface {
	// Denied reports whether the tenant rejects the write op names.
	Denied(ctx context.Context, id ID, op WriteOp) (bool, error)
}

// WriteOp names an operator write a WriteGuard protects. The set is closed.
type WriteOp int

const (
	// WriteDisable is disabling a tenant through SetStatus. It is the zero value,
	// so a value nobody chose reads as the destructive write.
	WriteDisable WriteOp = iota
	// WriteDelete is deleting a tenant through Delete.
	WriteDelete
)

// Provisioner creates a tenant together with its first administrator. A failure
// before the roster row removes what the call wrote; a failure after it leaves
// the row and returns the error. On ErrTenantExists the losing call removes only
// the rows it added.
type Provisioner interface {
	// Provision creates t and binds creator as its first tenant-admin, in this
	// order: the tenant-admin role and its grants, the roster row through
	// Catalog.Create, the role's builtin mark, then the creator's membership and
	// binding. Roster refusals stay readable through errors.Is.
	Provision(ctx context.Context, t Tenant, creator string) error
}
