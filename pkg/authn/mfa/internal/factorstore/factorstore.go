// Package factorstore holds the statements on authn_factors, the register of the second factors an account has, shared by the multi-factor module and its test-support package.
package factorstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/event"
)

// Table is the table of the register.
const Table = "authn_factors"

// SweepBatch is how many pending rows one statement of the sweep reads.
const SweepBatch = 1000

const factorColumns = "id, account_id, kind, label, status, created_at, activated_at, last_used_at"

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

// Store is the statements on authn_factors.
type Store struct {
	handle *gorm.DB
}

// New returns the register over one database handle.
func New(handle *gorm.DB) *Store { return &Store{handle: handle} }

func (g *Store) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

func recordsOf(rows []factorRow) []authn.FactorRecord {
	out := make([]authn.FactorRecord, len(rows))
	for i, r := range rows {
		out[i] = r.record()
	}
	return out
}

// List reads the account's rows of one status, or of every status when status is
// negative, oldest first.
func (g *Store) List(ctx context.Context, account authn.AccountID, status int) ([]authn.FactorRecord, error) {
	var rows []factorRow
	q := g.session(ctx).Table(Table).Select(factorColumns).Where("account_id = ?", string(account))
	if status >= 0 {
		q = q.Where("status = ?", status)
	}
	if err := q.Order("created_at, id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reading the factors of an account: %w", err)
	}
	return recordsOf(rows), nil
}

// Find reads one row of the account. found is false for a row that is not there
// and for one that belongs to another account, which the caller cannot tell
// apart.
func (g *Store) Find(ctx context.Context, account authn.AccountID, id string) (rec authn.FactorRecord, found bool, err error) {
	var rows []factorRow
	err = g.session(ctx).Table(Table).Select(factorColumns).
		Where("id = ? AND account_id = ?", id, string(account)).Limit(1).Scan(&rows).Error
	if err != nil {
		return authn.FactorRecord{}, false, fmt.Errorf("reading a factor: %w", err)
	}
	if len(rows) == 0 {
		return authn.FactorRecord{}, false, nil
	}
	return rows[0].record(), true, nil
}

// InsertPending writes a pending row of the kind after counting the account's rows of that kind that are not disabled. label is stored as given; a count at the limit is ErrFactorLimit.
func (g *Store) InsertPending(ctx context.Context, account authn.AccountID, kind authn.FactorKind, label string, limit int, besideActive bool, at time.Time) (authn.FactorRecord, error) {
	rec := authn.FactorRecord{
		ID:        event.NewID(),
		Account:   account,
		Kind:      kind,
		Label:     label,
		Status:    authn.FactorPending,
		CreatedAt: clock.UTC(at),
	}
	err := g.session(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		query := tx.Table(Table).Where("account_id = ? AND kind = ?", string(account), string(kind))
		if besideActive {
			query = query.Where("status = ?", int(authn.FactorPending))
		} else {
			query = query.Where("status <> ?", int(authn.FactorDisabled))
		}
		if err := query.Count(&count).Error; err != nil {
			return fmt.Errorf("counting the factors of a kind: %w", err)
		}
		if count >= int64(limit) {
			return authn.ErrFactorLimit
		}
		return tx.Exec("INSERT INTO "+Table+" (id, account_id, kind, label, status, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			rec.ID, string(account), string(kind), rec.Label, int(authn.FactorPending), rec.CreatedAt).Error
	})
	if err != nil {
		return authn.FactorRecord{}, err
	}
	return rec, nil
}

// Activate makes a pending row active at the given instant, only if it is still pending, and reports whether this call did.
func (g *Store) Activate(ctx context.Context, account authn.AccountID, id string, at time.Time) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+Table+" SET status = ?, activated_at = ? WHERE id = ? AND account_id = ? AND status = ?",
		int(authn.FactorActive), clock.UTC(at), id, string(account), int(authn.FactorPending))
	if res.Error != nil {
		return false, fmt.Errorf("activating a factor: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// ActivateReplacing makes a pending row active and disables the account's other active rows of the same kind in one transaction, only when the activation won.
func (g *Store) ActivateReplacing(ctx context.Context, account authn.AccountID, id string, kind authn.FactorKind, at time.Time) (bool, error) {
	won := false
	err := g.session(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec("UPDATE "+Table+" SET status = ?, activated_at = ? WHERE id = ? AND account_id = ? AND status = ?",
			int(authn.FactorActive), clock.UTC(at), id, string(account), int(authn.FactorPending))
		if res.Error != nil {
			return fmt.Errorf("activating a factor: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			return nil
		}
		won = true
		return tx.Exec("UPDATE "+Table+" SET status = ? WHERE account_id = ? AND kind = ? AND status = ? AND id <> ?",
			int(authn.FactorDisabled), string(account), string(kind), int(authn.FactorActive), id).Error
	})
	if err != nil {
		return false, fmt.Errorf("activating a replacing factor: %w", err)
	}
	return won, nil
}

// UpdateLabel writes the label of one row of the account and reports whether there was one. Another account's row changes nothing.
func (g *Store) UpdateLabel(ctx context.Context, account authn.AccountID, id, label string) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+Table+" SET label = ? WHERE id = ? AND account_id = ?",
		label, id, string(account))
	if res.Error != nil {
		return false, fmt.Errorf("renaming a factor: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// Touch records the instant a factor last passed a verification.
func (g *Store) Touch(ctx context.Context, id string, at time.Time) error {
	if err := g.session(ctx).Exec("UPDATE "+Table+" SET last_used_at = ? WHERE id = ?", clock.UTC(at), id).Error; err != nil {
		return fmt.Errorf("recording the use of a factor: %w", err)
	}
	return nil
}

// Remove deletes one row of the account and says whether there was one.
func (g *Store) Remove(ctx context.Context, account authn.AccountID, id string) (bool, error) {
	res := g.session(ctx).Exec("DELETE FROM "+Table+" WHERE id = ? AND account_id = ?", id, string(account))
	if res.Error != nil {
		return false, fmt.Errorf("deleting a factor: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// RemovePending deletes a row that is still pending, leaving one that changed status alone.
func (g *Store) RemovePending(ctx context.Context, id string) (bool, error) {
	res := g.session(ctx).Exec("DELETE FROM "+Table+" WHERE id = ? AND status = ?", id, int(authn.FactorPending))
	if res.Error != nil {
		return false, fmt.Errorf("deleting a pending factor: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// DisableAll disables every row of the account that is not disabled, in one transaction, and returns how many it changed.
func (g *Store) DisableAll(ctx context.Context, account authn.AccountID) (int, error) {
	var changed int64
	err := g.session(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec("UPDATE "+Table+" SET status = ? WHERE account_id = ? AND status <> ?",
			int(authn.FactorDisabled), string(account), int(authn.FactorDisabled))
		changed = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return 0, fmt.Errorf("disabling the factors of an account: %w", err)
	}
	return int(changed), nil
}

// Disable disables one active row of the account, only if it is still active.
func (g *Store) Disable(ctx context.Context, account authn.AccountID, id string) error {
	res := g.session(ctx).Exec("UPDATE "+Table+" SET status = ? WHERE id = ? AND account_id = ? AND status = ?",
		int(authn.FactorDisabled), id, string(account), int(authn.FactorActive))
	if res.Error != nil {
		return fmt.Errorf("disabling a factor: %w", res.Error)
	}
	return nil
}

// Seasoned reports whether the account has an active row of a kind other than
// SMS that was activated at or before the cutoff.
func (g *Store) Seasoned(ctx context.Context, account authn.AccountID, cutoff time.Time) (bool, error) {
	var found int64
	err := g.session(ctx).Table(Table).
		Where("account_id = ? AND kind <> ? AND status = ? AND activated_at IS NOT NULL AND activated_at <= ?",
			string(account), string(authn.FactorSMS), int(authn.FactorActive), clock.UTC(cutoff)).
		Limit(1).Count(&found).Error
	if err != nil {
		return false, fmt.Errorf("reading the age of the account's factors: %w", err)
	}
	return found > 0, nil
}

// ActiveKinds lists the kinds any account has an active row of.
func (g *Store) ActiveKinds(ctx context.Context) ([]authn.FactorKind, error) {
	var kinds []string
	if err := g.session(ctx).Table(Table).Where("status = ?", int(authn.FactorActive)).
		Distinct().Order("kind").Pluck("kind", &kinds).Error; err != nil {
		return nil, fmt.Errorf("reading the kinds of the active factors: %w", err)
	}
	out := make([]authn.FactorKind, len(kinds))
	for i, k := range kinds {
		out[i] = authn.FactorKind(k)
	}
	return out, nil
}

// Probe reports whether the register's table can be read. An error that is not
// ErrNotMigrated says the table is there and the read failed.
func (g *Store) Probe(ctx context.Context) error {
	err := g.session(ctx).Exec("SELECT 1 FROM " + Table + " WHERE 1 = 0").Error
	if err == nil {
		return nil
	}
	if !g.session(ctx).Migrator().HasTable(Table) {
		return fmt.Errorf("%w: %s is not there: %w", authn.ErrNotMigrated, Table, err)
	}
	return fmt.Errorf("reading %s: %w", Table, err)
}

// ExpiredPending reads a batch of pending rows created before the cutoff.
func (g *Store) ExpiredPending(ctx context.Context, cutoff time.Time) ([]authn.FactorRecord, error) {
	var rows []factorRow
	err := g.session(ctx).Table(Table).Select(factorColumns).
		Where("status = ? AND created_at < ?", int(authn.FactorPending), clock.UTC(cutoff)).
		Order("created_at, id").Limit(SweepBatch).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the pending factors past their time: %w", err)
	}
	return recordsOf(rows), nil
}
