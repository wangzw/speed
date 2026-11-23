package mfa

// The register's statements live in pkg/authn/mfa/internal/factorstore, so
// that the module's test-support package mfatest can write and read the same
// rows through them: a fixture that wrote raw SQL of its own could outlive a
// rename of a column, and the module's own register is unexported. The type
// below is this module's view of those statements; it decides nothing itself.
//
// factorTable stays here because the module's tests name the table directly.

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/mfa/internal/factorstore"
)

// factorTable is the table of the register, and sweepBatch is how many pending
// rows the sweep reads in one statement. Both stay here because the module and
// its tests name them.
const (
	factorTable = factorstore.Table
	sweepBatch  = factorstore.SweepBatch
)

// register is the factor register: the statements on authn_factors and nothing
// else. It decides nothing; the orchestration around it does.
type register struct {
	handle *gorm.DB
}

func (g *register) store() *factorstore.Store { return factorstore.New(g.handle) }

// list reads the account's rows of one status, or of every status when status is
// negative, oldest first.
func (g *register) list(ctx context.Context, account authn.AccountID, status int) ([]authn.FactorRecord, error) {
	return g.store().List(ctx, account, status)
}

// find reads one row of the account. found is false for a row that is not there
// and for one that belongs to another account, which the caller cannot tell
// apart.
func (g *register) find(ctx context.Context, account authn.AccountID, id string) (authn.FactorRecord, bool, error) {
	return g.store().Find(ctx, account, id)
}

// insertPending writes a pending row of the kind, after counting the account's
// rows of that kind that are not disabled. label is stored as given, so an empty
// one is a factor with no name. The count and the insert are one transaction; a
// disabled row, which only a reset leaves behind, does not count, or an account
// whose factors were all reset could not register another once it held the
// limit. A count at the limit is ErrFactorLimit.
func (g *register) insertPending(ctx context.Context, account authn.AccountID, kind authn.FactorKind, label string, limit int, besideActive bool, at time.Time) (authn.FactorRecord, error) {
	return g.store().InsertPending(ctx, account, kind, label, limit, besideActive, at)
}

// activate makes a pending row active, at the instant at. The statement is
// conditional on the row still being pending, so a sweep, a reset or a removal
// that came first wins, and the result says whether this call did.
func (g *register) activate(ctx context.Context, account authn.AccountID, id string, at time.Time) (bool, error) {
	return g.store().Activate(ctx, account, id, at)
}

// activateReplacing makes a pending row active and, in the same transaction,
// disables the account's other active rows of the same kind -- the factor the
// registration replaces. The replacement happens only when the activation won,
// so a row a sweep, a reset or a removal took first leaves the old factor
// active, and the old row keeps its data for the audit trail.
func (g *register) activateReplacing(ctx context.Context, account authn.AccountID, id string, kind authn.FactorKind, at time.Time) (bool, error) {
	return g.store().ActivateReplacing(ctx, account, id, kind, at)
}

// updateLabel writes the label of one row of the account and says whether there
// was one. A row of another account changes nothing and reports false, so the
// caller cannot tell it apart from a row that is not there.
func (g *register) updateLabel(ctx context.Context, account authn.AccountID, id, label string) (bool, error) {
	return g.store().UpdateLabel(ctx, account, id, label)
}

// touch records the instant a factor last passed a verification.
func (g *register) touch(ctx context.Context, id string, at time.Time) error {
	return g.store().Touch(ctx, id, at)
}

// remove deletes one row of the account and says whether there was one.
func (g *register) remove(ctx context.Context, account authn.AccountID, id string) (bool, error) {
	return g.store().Remove(ctx, account, id)
}

// removePending deletes a row that is still pending. A row that has become
// anything else in the meantime is left alone, which is what keeps the sweep and
// a registration that is finishing from deleting each other's work.
func (g *register) removePending(ctx context.Context, id string) (bool, error) {
	return g.store().RemovePending(ctx, id)
}

// disableAll sets every row of the account that is not yet disabled to disabled,
// in one transaction, and returns how many it changed. A row already disabled is
// not counted, so a second call that finds nothing to do returns zero.
func (g *register) disableAll(ctx context.Context, account authn.AccountID) (int, error) {
	return g.store().DisableAll(ctx, account)
}

// disable sets one active row of the account to disabled. The update is
// conditional on the row still being active, so a second detection of the same
// clone, or a reset or a removal that came first, is a no-op.
func (g *register) disable(ctx context.Context, account authn.AccountID, id string) error {
	return g.store().Disable(ctx, account, id)
}

// seasoned reports whether the account has an active row of a kind other than
// SMS that was activated at or before the cutoff.
func (g *register) seasoned(ctx context.Context, account authn.AccountID, cutoff time.Time) (bool, error) {
	return g.store().Seasoned(ctx, account, cutoff)
}

// activeKinds lists the kinds any account has an active row of.
func (g *register) activeKinds(ctx context.Context) ([]authn.FactorKind, error) {
	return g.store().ActiveKinds(ctx)
}

// probe reports whether the register's table can be read. An error that is not
// ErrNotMigrated says the table is there and the read failed.
func (g *register) probe(ctx context.Context) error {
	return g.store().Probe(ctx)
}

// expiredPending reads a batch of pending rows created before the cutoff.
func (g *register) expiredPending(ctx context.Context, cutoff time.Time) ([]authn.FactorRecord, error) {
	return g.store().ExpiredPending(ctx, cutoff)
}
