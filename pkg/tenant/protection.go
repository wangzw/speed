package tenant

import "context"

// WriteGuard reports whether a tenant rejects an operator write, a host policy the management
// surface asks before it writes. A refused write is not carried out, and an answer that cannot be
// read is a refusal, never permission. It acts on the operator's write paths alone.
type WriteGuard interface {
	// Denied reports whether the tenant rejects the write op names. An error
	// means the answer could not be read, and a caller treats it as a refusal.
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

// Provisioner creates a tenant together with its first administrator: the tenant's role, its grants, the
// roster row, the role's builtin mark, then the creator's membership and binding. A failure before the
// roster row removes what the call wrote, and a failure after leaves the row in place.
type Provisioner interface {
	// Provision creates t and binds creator as its first tenant-admin. Roster
	// refusals stay readable through errors.Is: ErrTenantExists when the identifier is
	// taken, and ErrNameConflict when another tenant holds the name.
	Provision(ctx context.Context, t Tenant, creator string) error
}
