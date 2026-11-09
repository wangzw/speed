package tenant

import "context"

// WriteGuard reports whether a tenant rejects an operator write.
//
// The roster knows neither the platform domain nor a personal tenant: which
// tenants may not be disabled or deleted is a host policy, so the answer is a
// seam the host implements and the management surface asks before it writes. A
// write the guard refuses is not carried out; an answer that cannot be read is
// answered by the caller with a 503 and is never read as permission. The answer
// only narrows: it cannot make a write the roster refuses happen.
//
// It acts on the operator's write paths alone. SetStatusByAccount is the
// account lifecycle's transition — a personal tenant follows the account that
// owns it — and it does not consult this seam, so a tenant an operator may not
// disable still follows its account.
type WriteGuard interface {
	// Denied reports whether the tenant rejects the write op names. An error
	// means the answer could not be read, and a caller treats it as a refusal
	// to proceed rather than a pass.
	Denied(ctx context.Context, id ID, op WriteOp) (bool, error)
}

// WriteOp names an operator write a WriteGuard protects. The set is closed.
type WriteOp int

const (
	// WriteDisable is disabling a tenant through SetStatus. It is the zero
	// value, so a value nobody chose reads as the destructive write.
	WriteDisable WriteOp = iota
	// WriteDelete is deleting a tenant through Delete.
	WriteDelete
)

// Provisioner creates a tenant together with its first administrator.
//
// Opening a tenant is not an isolated insert: its creator is the tenant's first
// tenant-admin, so the tenant's role, that role's grants and the creator's
// binding have to exist with the roster row. The roster knows no role and no
// grant, so the cross-capability write is the host's, and the management
// surface's registration route calls this seam instead of Catalog.Create.
//
// An implementation writes the tenant domain's tenant-admin role, its grants,
// the roster row through Catalog.Create, the role's builtin mark, and then the
// creator's membership and the creator's binding. The roster row precedes the
// administrator rows so a failure after it leaves a tenant the host's repair
// path can reconcile, and the builtin mark precedes them so a failure at the
// mark leaves a tenant with a non-builtin role rather than a half-written
// administration. A failure before the row removes what the call wrote; a
// failure after it leaves the row in place and returns the error, so the caller
// answers 503 and the call never reports success. Repair after such a failure is
// the host's decision, not this seam's.
//
// ErrTenantExists is the exception: that identifier was registered by another
// replica between the check and the write, so the domain is the winner's. The
// losing call removes only the rows it added, leaves the winner's policy
// untouched, and does not mark the role builtin.
type Provisioner interface {
	// Provision creates t and binds creator as its first tenant-admin. Roster
	// refusals stay readable through errors.Is: ErrTenantExists when the
	// identifier is taken, and ErrNameConflict when another tenant holds the
	// name.
	Provision(ctx context.Context, t Tenant, creator string) error
}
