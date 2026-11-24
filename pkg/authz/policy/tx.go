package policy

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	tenancy "github.com/vislake/speed/pkg/tenant"
)

// TxManager is the manager's transaction-aware write surface, for a caller that
// already holds a transaction and must write policy rows inside it.
//
// Each writes through the handle it is handed and returns the work that belongs
// after the caller's transaction has committed: the snapshot reload and the
// change event, in that order. A write that changed nothing returns a nil
// closure. A refusal is the same refusal the corresponding Manager method gives.
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
	// DeleteTenantPolicyTx removes every role, grant and binding of one tenant
	// inside the caller's transaction. The builtin mark does not protect its
	// tenant-admin role from this write, and the last-administrator floor does
	// not apply. after is nil when the tenant held no policy row.
	DeleteTenantPolicyTx(ctx context.Context, tx *gorm.DB, tenant string) (after func(context.Context) error, err error)
}

var _ TxManager = (*product)(nil)

func crossingTx(ctx context.Context, tx *gorm.DB) *gorm.DB {
	return tx.WithContext(tenancy.CrossTenant(ctx))
}

// afterWriteTx is the work a tx write hands back: the snapshot reload and the change event, in that order, once the caller commits.
func (p *product) afterWriteTx() func(context.Context) error {
	return func(ctx context.Context) error { return p.afterWrite(ctx) }
}

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

func (p *product) DeleteTenantPolicyTx(ctx context.Context, tx *gorm.DB, tenant string) (func(context.Context) error, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	handle := crossingTx(ctx, tx)
	var changed int64
	for _, model := range []any{&grantRow{}, &bindingRow{}, &roleRow{}} {
		res := handle.Where("tenant_id = ?", tenant).Delete(model)
		if res.Error != nil {
			return nil, fmt.Errorf("authz/policy: deleting tenant %q's policy rows: %w", tenant, res.Error)
		}
		changed += res.RowsAffected
	}
	if changed == 0 {
		return nil, nil
	}
	return p.afterWriteTx(), nil
}
