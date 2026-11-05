package policy

import (
	"context"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	tenancy "github.com/vislake/speed/pkg/tenant"
)

// TxManager is the manager's transaction-aware write surface.
//
// The Manager writes open a transaction of this module's own, which is what the
// management surface and the host's seeding steps want. An account's creation
// is the other case: the roles, grants and bindings a new organization gets
// have to be written in the account's transaction, so that a failure on either
// side leaves neither. A caller that already holds a transaction takes these
// methods instead.
//
// Each writes through the handle it is handed and nothing else, and returns the
// work that belongs after the caller's transaction has committed: the snapshot
// reload and the change event, in that order. The caller runs the closure once
// it has committed. A closure run inside the transaction would reload a
// snapshot that does not yet see the write, and announce a change that may
// still roll back. A write that changed nothing returns a nil closure.
//
// A refusal is the same refusal the corresponding Manager method gives: the two
// share their checks, so a caller switching between them does not change what
// is accepted.
type TxManager interface {
	// PutRoleTx is PutRole inside the caller's transaction.
	PutRoleTx(ctx context.Context, tx *gorm.DB, r authz.RoleRecord) (after func(context.Context) error, err error)
	// DeleteRoleTx is DeleteRole inside the caller's transaction.
	DeleteRoleTx(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) (after func(context.Context) error, err error)
	// SeedGrantsTx is SeedGrants inside the caller's transaction. added is the
	// number of grants this call wrote, and after is nil when it wrote none.
	SeedGrantsTx(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role, grants []authz.Grant) (added int, after func(context.Context) error, err error)
	// PutBindingTx is PutBinding inside the caller's transaction.
	PutBindingTx(ctx context.Context, tx *gorm.DB, b authz.Binding) (after func(context.Context) error, err error)
	// ClearAccountBindingsTx is ClearAccountBindings inside the caller's
	// transaction, and after is nil when it removed nothing.
	ClearAccountBindingsTx(ctx context.Context, tx *gorm.DB, tenant, account string) (after func(context.Context) error, err error)
}

var _ TxManager = (*product)(nil)

// crossingTx marks the transaction's statements as naming their own tenant, the
// way crossing marks a session of the product's own handle.
func crossingTx(ctx context.Context, tx *gorm.DB) *gorm.DB {
	return tx.WithContext(tenancy.CrossTenant(ctx))
}

// afterWriteTx is the work a tx write hands back: the snapshot reload and the
// change event, in that order, once the caller's transaction has committed.
func (p *product) afterWriteTx() func(context.Context) error {
	return func(ctx context.Context) error { return p.afterWrite(ctx) }
}

// PutRoleTx implements TxManager.
func (p *product) PutRoleTx(ctx context.Context, tx *gorm.DB, r authz.RoleRecord) (func(context.Context) error, error) {
	row, err := roleRowFor(r)
	if err != nil {
		return nil, err
	}
	changed, err := putRoleIn(ctx, crossingTx(ctx, tx), r, row)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return p.afterWriteTx(), nil
}

// DeleteRoleTx implements TxManager.
func (p *product) DeleteRoleTx(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) (func(context.Context) error, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	removed, err := deleteRoleIn(ctx, crossingTx(ctx, tx), tenant, role)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, nil
	}
	return p.afterWriteTx(), nil
}

// SeedGrantsTx implements TxManager.
func (p *product) SeedGrantsTx(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role, grants []authz.Grant) (int, func(context.Context) error, error) {
	if err := requireTenant(tenant); err != nil {
		return 0, nil, err
	}
	added, err := seedGrantsIn(ctx, crossingTx(ctx, tx), tenant, role, grants)
	if err != nil {
		return 0, nil, err
	}
	if added == 0 {
		return 0, nil, nil
	}
	return added, p.afterWriteTx(), nil
}

// PutBindingTx implements TxManager.
func (p *product) PutBindingTx(ctx context.Context, tx *gorm.DB, b authz.Binding) (func(context.Context) error, error) {
	if err := p.checkBinding(ctx, b); err != nil {
		return nil, err
	}
	changed, err := putBindingIn(ctx, crossingTx(ctx, tx), b)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return p.afterWriteTx(), nil
}

// ClearAccountBindingsTx implements TxManager.
func (p *product) ClearAccountBindingsTx(ctx context.Context, tx *gorm.DB, tenant, account string) (func(context.Context) error, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	removed, err := clearAccountBindingsIn(ctx, crossingTx(ctx, tx), tenant, account)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, nil
	}
	return p.afterWriteTx(), nil
}
