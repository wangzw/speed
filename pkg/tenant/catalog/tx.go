package catalog

import (
	"context"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// TxCatalog is the roster's transaction-aware write surface.
//
// The Catalog writes open a connection and a transaction of their own, which is
// what a host command or the registration route wants: the caller has no
// transaction to join. An account's creation is the other case — a user
// account's personal tenant has to be written in the account's transaction, so
// that a failure on either side leaves neither — and a caller that already
// holds a transaction takes these two methods instead.
//
// Each writes through the handle it is handed and nothing else. It returns the
// work that belongs after the caller's transaction has committed — the roster's
// whole reread, which is how this replica starts serving the write — and nil
// when it wrote nothing. The caller runs the closure once it has committed; a
// closure run inside the transaction would reread the table before the write is
// visible to any other connection, and on a database held to one connection
// would wait for the transaction that called it.
//
// A failure is reported as it stands. The context-taking writes classify a
// driver failure by rereading the table on a connection outside the transaction;
// inside a caller's transaction that read is neither possible nor meaningful,
// so a caller that needs the classification reads its own transaction's state.
type TxCatalog interface {
	// CreateTx is Create inside the caller's transaction.
	CreateTx(ctx context.Context, tx *gorm.DB, t tenant.Tenant) (after func(context.Context) error, err error)
	// DeleteTx is Delete inside the caller's transaction.
	DeleteTx(ctx context.Context, tx *gorm.DB, id tenant.ID) (after func(context.Context) error, err error)
}

var _ TxCatalog = (*roster)(nil)

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

// afterTxWrite is the work a tx write hands back: the whole roster reread, once
// the caller's transaction has committed. A write that changed nothing has no
// result for this process to serve.
func (r *roster) afterTxWrite(wrote bool) func(context.Context) error {
	if !wrote {
		return nil
	}
	return func(ctx context.Context) error { return r.afterWrite(ctx) }
}
