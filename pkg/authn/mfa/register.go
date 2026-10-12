package mfa

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/event"
)

// factorTable is the table of the register.
const factorTable = "authn_factors"

// sweepBatch is how many pending rows one statement of the sweep reads.
const sweepBatch = 1000

// The columns of the register as a read names them.
const factorColumns = "id, account_id, kind, label, status, created_at, activated_at, last_used_at"

// factorRow is one row of the register as a read returns it. The times are
// pointers where the column is nullable, and every time read is converted to UTC
// on the way out.
type factorRow struct {
	ID          string
	AccountID   string
	Kind        string
	Label       string
	Status      int
	CreatedAt   time.Time
	ActivatedAt *time.Time
	LastUsedAt  *time.Time
}

// record is the row as the root package's FactorRecord, which holds nothing of
// the kind's own data. A status outside the three the column admits reads as
// disabled: a value nobody chose is not one to verify with.
func (r factorRow) record() authn.FactorRecord {
	rec := authn.FactorRecord{
		ID:        r.ID,
		Account:   authn.AccountID(r.AccountID),
		Kind:      authn.FactorKind(r.Kind),
		Label:     r.Label,
		CreatedAt: clock.UTC(r.CreatedAt),
	}
	switch r.Status {
	case int(authn.FactorPending):
		rec.Status = authn.FactorPending
	case int(authn.FactorActive):
		rec.Status = authn.FactorActive
	default:
		rec.Status = authn.FactorDisabled
	}
	if r.ActivatedAt != nil {
		rec.ActivatedAt = clock.UTC(*r.ActivatedAt)
	}
	if r.LastUsedAt != nil {
		rec.LastUsedAt = clock.UTC(*r.LastUsedAt)
	}
	return rec
}

// register is the factor register: the statements on authn_factors and nothing
// else. It decides nothing; the orchestration around it does.
type register struct {
	handle *gorm.DB
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

func recordsOf(rows []factorRow) []authn.FactorRecord {
	out := make([]authn.FactorRecord, len(rows))
	for i, r := range rows {
		out[i] = r.record()
	}
	return out
}

// list reads the account's rows of one status, or of every status when status is
// negative, oldest first.
func (g *register) list(ctx context.Context, account authn.AccountID, status int) ([]authn.FactorRecord, error) {
	var rows []factorRow
	q := g.session(ctx).Table(factorTable).Select(factorColumns).Where("account_id = ?", string(account))
	if status >= 0 {
		q = q.Where("status = ?", status)
	}
	if err := q.Order("created_at, id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reading the factors of an account: %w", err)
	}
	return recordsOf(rows), nil
}

// find reads one row of the account. found is false for a row that is not there
// and for one that belongs to another account, which the caller cannot tell
// apart.
func (g *register) find(ctx context.Context, account authn.AccountID, id string) (rec authn.FactorRecord, found bool, err error) {
	var rows []factorRow
	err = g.session(ctx).Table(factorTable).Select(factorColumns).
		Where("id = ? AND account_id = ?", id, string(account)).Limit(1).Scan(&rows).Error
	if err != nil {
		return authn.FactorRecord{}, false, fmt.Errorf("reading a factor: %w", err)
	}
	if len(rows) == 0 {
		return authn.FactorRecord{}, false, nil
	}
	return rows[0].record(), true, nil
}

// insertPending writes a pending row of the kind, after counting the account's
// rows of that kind that are not disabled. The count and the insert are one
// transaction; a disabled row, which only a reset leaves behind, does not count,
// or an account whose factors were all reset could not register another once it
// held the limit. A count at the limit is ErrFactorLimit.
func (g *register) insertPending(ctx context.Context, account authn.AccountID, kind authn.FactorKind, limit int, at time.Time) (authn.FactorRecord, error) {
	rec := authn.FactorRecord{
		ID:        event.NewID(),
		Account:   account,
		Kind:      kind,
		Status:    authn.FactorPending,
		CreatedAt: clock.UTC(at),
	}
	err := g.session(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Table(factorTable).
			Where("account_id = ? AND kind = ? AND status <> ?", string(account), string(kind), int(authn.FactorDisabled)).
			Count(&count).Error; err != nil {
			return fmt.Errorf("counting the factors of a kind: %w", err)
		}
		if count >= int64(limit) {
			return authn.ErrFactorLimit
		}
		return tx.Exec("INSERT INTO "+factorTable+" (id, account_id, kind, label, status, created_at) VALUES (?, ?, ?, '', ?, ?)",
			rec.ID, string(account), string(kind), int(authn.FactorPending), rec.CreatedAt).Error
	})
	if err != nil {
		return authn.FactorRecord{}, err
	}
	return rec, nil
}

// activate makes a pending row active, at the instant at. The statement is
// conditional on the row still being pending, so a sweep, a reset or a removal
// that came first wins, and the result says whether this call did.
func (g *register) activate(ctx context.Context, account authn.AccountID, id string, at time.Time) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+factorTable+" SET status = ?, activated_at = ? WHERE id = ? AND account_id = ? AND status = ?",
		int(authn.FactorActive), clock.UTC(at), id, string(account), int(authn.FactorPending))
	if res.Error != nil {
		return false, fmt.Errorf("activating a factor: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// touch records the instant a factor last passed a verification.
func (g *register) touch(ctx context.Context, id string, at time.Time) error {
	if err := g.session(ctx).Exec("UPDATE "+factorTable+" SET last_used_at = ? WHERE id = ?", clock.UTC(at), id).Error; err != nil {
		return fmt.Errorf("recording the use of a factor: %w", err)
	}
	return nil
}

// remove deletes one row of the account and says whether there was one.
func (g *register) remove(ctx context.Context, account authn.AccountID, id string) (bool, error) {
	res := g.session(ctx).Exec("DELETE FROM "+factorTable+" WHERE id = ? AND account_id = ?", id, string(account))
	if res.Error != nil {
		return false, fmt.Errorf("deleting a factor: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// removePending deletes a row that is still pending. A row that has become
// anything else in the meantime is left alone, which is what keeps the sweep and
// a registration that is finishing from deleting each other's work.
func (g *register) removePending(ctx context.Context, id string) (bool, error) {
	res := g.session(ctx).Exec("DELETE FROM "+factorTable+" WHERE id = ? AND status = ?", id, int(authn.FactorPending))
	if res.Error != nil {
		return false, fmt.Errorf("deleting a pending factor: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// disableAll sets every row of the account that is not yet disabled to disabled,
// in one transaction, and returns how many it changed. A row already disabled is
// not counted, so a second call that finds nothing to do returns zero.
func (g *register) disableAll(ctx context.Context, account authn.AccountID) (int, error) {
	var changed int64
	err := g.session(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec("UPDATE "+factorTable+" SET status = ? WHERE account_id = ? AND status <> ?",
			int(authn.FactorDisabled), string(account), int(authn.FactorDisabled))
		changed = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return 0, fmt.Errorf("disabling the factors of an account: %w", err)
	}
	return int(changed), nil
}

// disable sets one active row of the account to disabled. The update is
// conditional on the row still being active, so a second detection of the same
// clone, or a reset or a removal that came first, is a no-op.
func (g *register) disable(ctx context.Context, account authn.AccountID, id string) error {
	res := g.session(ctx).Exec("UPDATE "+factorTable+" SET status = ? WHERE id = ? AND account_id = ? AND status = ?",
		int(authn.FactorDisabled), id, string(account), int(authn.FactorActive))
	if res.Error != nil {
		return fmt.Errorf("disabling a factor: %w", res.Error)
	}
	return nil
}

// seasoned reports whether the account has an active row of a kind other than
// SMS that was activated at or before the cutoff.
func (g *register) seasoned(ctx context.Context, account authn.AccountID, cutoff time.Time) (bool, error) {
	var found int64
	err := g.session(ctx).Table(factorTable).
		Where("account_id = ? AND kind <> ? AND status = ? AND activated_at IS NOT NULL AND activated_at <= ?",
			string(account), string(authn.FactorSMS), int(authn.FactorActive), clock.UTC(cutoff)).
		Limit(1).Count(&found).Error
	if err != nil {
		return false, fmt.Errorf("reading the age of the account's factors: %w", err)
	}
	return found > 0, nil
}

// activeKinds lists the kinds any account has an active row of.
func (g *register) activeKinds(ctx context.Context) ([]authn.FactorKind, error) {
	var kinds []string
	if err := g.session(ctx).Table(factorTable).Where("status = ?", int(authn.FactorActive)).
		Distinct().Order("kind").Pluck("kind", &kinds).Error; err != nil {
		return nil, fmt.Errorf("reading the kinds of the active factors: %w", err)
	}
	out := make([]authn.FactorKind, len(kinds))
	for i, k := range kinds {
		out[i] = authn.FactorKind(k)
	}
	return out, nil
}

// probe reports whether the register's table can be read. An error that is not
// ErrNotMigrated says the table is there and the read failed.
func (g *register) probe(ctx context.Context) error {
	err := g.session(ctx).Exec("SELECT 1 FROM " + factorTable + " WHERE 1 = 0").Error
	if err == nil {
		return nil
	}
	if !g.session(ctx).Migrator().HasTable(factorTable) {
		return fmt.Errorf("%w: %s is not there: %w", authn.ErrNotMigrated, factorTable, err)
	}
	return fmt.Errorf("reading %s: %w", factorTable, err)
}

// expiredPending reads a batch of pending rows created before the cutoff.
func (g *register) expiredPending(ctx context.Context, cutoff time.Time) ([]authn.FactorRecord, error) {
	var rows []factorRow
	err := g.session(ctx).Table(factorTable).Select(factorColumns).
		Where("status = ? AND created_at < ?", int(authn.FactorPending), clock.UTC(cutoff)).
		Order("created_at, id").Limit(sweepBatch).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the pending factors past their time: %w", err)
	}
	return recordsOf(rows), nil
}
