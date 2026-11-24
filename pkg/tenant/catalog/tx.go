package catalog

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// TxCatalog is the roster's write surface for a caller that already holds a
// transaction. Each method writes through the handle it is given and returns
// the work to run after that transaction has committed (the roster reread), or
// nil when nothing was written; running it inside the transaction would reread
// before the write is visible and can deadlock on a single-connection database.
// Driver failures are reported as they stand, without the reread classification
// the context-taking writes do.
type TxCatalog interface {
	// GetTx reads one tenant through the caller's transaction; the second result
	// is false when no row carries the identifier.
	GetTx(ctx context.Context, tx *gorm.DB, id tenant.ID) (tenant.Tenant, bool, error)
	// CreateTx is Create inside the caller's transaction.
	CreateTx(ctx context.Context, tx *gorm.DB, t tenant.Tenant) (after func(context.Context) error, err error)
	// DeleteTx is Delete inside the caller's transaction.
	DeleteTx(ctx context.Context, tx *gorm.DB, id tenant.ID) (after func(context.Context) error, err error)
	// SetOwnerTx is SetOwner inside the caller's transaction.
	SetOwnerTx(ctx context.Context, tx *gorm.DB, id tenant.ID, owner string) (after func(context.Context) error, err error)
}

var _ TxCatalog = (*roster)(nil)

// GetTx implements TxCatalog.
func (r *roster) GetTx(_ context.Context, tx *gorm.DB, id tenant.ID) (tenant.Tenant, bool, error) {
	if err := checkField("identifier", string(id)); err != nil {
		return tenant.Tenant{}, false, err
	}
	held, err := lookUp(tx, "id", string(id))
	if err != nil {
		return tenant.Tenant{}, false, err
	}
	if len(held) == 0 {
		return tenant.Tenant{}, false, nil
	}
	return tenantOf(held[0]), true, nil
}

// CreateTx implements TxCatalog.
func (r *roster) CreateTx(ctx context.Context, tx *gorm.DB, t tenant.Tenant) (func(context.Context) error, error) {
	if err := checkWrite(t); err != nil {
		return nil, err
	}
	wrote, err := r.createIn(ctx, tx, t)
	if err != nil {
		return nil, err
	}
	return r.afterTxWrite(wrote), nil
}

// DeleteTx implements TxCatalog.
func (r *roster) DeleteTx(ctx context.Context, tx *gorm.DB, id tenant.ID) (func(context.Context) error, error) {
	if err := checkField("identifier", string(id)); err != nil {
		return nil, err
	}
	wrote, err := r.deleteIn(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return r.afterTxWrite(wrote), nil
}

// SetOwnerTx implements TxCatalog.
func (r *roster) SetOwnerTx(_ context.Context, tx *gorm.DB, id tenant.ID, owner string) (func(context.Context) error, error) {
	if err := checkField("identifier", string(id)); err != nil {
		return nil, err
	}
	if err := checkField("owner", owner); err != nil {
		return nil, err
	}
	held, err := lookUp(tx, "id", string(id))
	if err != nil {
		return nil, err
	}
	if len(held) == 0 {
		return nil, fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
	}
	if tenant.Status(held[0].Status) == tenant.StatusDeleted {
		return nil, tombstone(id)
	}
	if held[0].OwnerAccount == owner {
		return nil, nil
	}
	written := tx.Model(&row{}).
		Where("id = ? AND status <> ?", string(id), int(tenant.StatusDeleted)).
		Update("owner_account", owner)
	if written.Error != nil {
		return nil, written.Error
	}
	if written.RowsAffected > 0 {
		return r.afterTxWrite(true), nil
	}
	if err := missingOrTombstone(tx, id); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *roster) afterTxWrite(wrote bool) func(context.Context) error {
	if !wrote {
		return nil
	}
	return func(ctx context.Context) error { return r.afterWrite(ctx) }
}
