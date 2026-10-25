package sms

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/db"
)

// sweepBatch is how many expired rows one statement of the sweep removes.
const sweepBatch = 1000

// challengeRow is one row of the challenge table as a read returns it. Every
// time read is converted to UTC on the way out.
type challengeRow struct {
	ID          string
	Purpose     string
	PhoneMAC    string
	SourceMAC   *string
	AccountID   *string
	Suppressed  bool
	CodeMAC     string
	Attempts    int
	MaxAttempts int
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
}

// register is the challenge table: the statements on authn_challenges and
// nothing else. It decides nothing; the flow around it does.
type register struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

// insert writes the new row and, in the same transaction, voids the rows for the
// same number and purpose that are still live: a resend does not leave two codes
// valid at once. A voided row is one whose consumed_at is set, which is the same
// state the check reads as spent. The void is conditional on the column still
// being empty and the row not being past its time, so a row another request
// already consumed or that has expired is left as it is.
//
// The void, the write and, when guard is set, the guard run in one transaction
// under lock: the guard is where the budget check lives, so the counts it reads
// and the row it guards are serialized against every send that shares a
// dimension. A guard error rolls the transaction back, so a refused send writes
// no row.
func (g *register) insert(ctx context.Context, row challengeRow, guard func(*gorm.DB) error) error {
	return g.session(ctx).Transaction(func(tx *gorm.DB) error {
		if err := g.lock(tx, row); err != nil {
			return err
		}
		if guard != nil {
			if err := guard(tx); err != nil {
				return err
			}
		}
		if err := tx.Exec("UPDATE "+challengeTable+" SET consumed_at = ? "+
			"WHERE phone_mac = ? AND purpose = ? AND consumed_at IS NULL AND expires_at > ?",
			clock.UTC(row.CreatedAt), row.PhoneMAC, row.Purpose, clock.UTC(row.CreatedAt)).Error; err != nil {
			return fmt.Errorf("voiding the previous challenges of a number: %w", err)
		}
		var account any
		if row.AccountID != nil {
			account = *row.AccountID
		}
		var source any
		if row.SourceMAC != nil {
			source = *row.SourceMAC
		}
		if err := tx.Exec("INSERT INTO "+challengeTable+
			" (id, purpose, phone_mac, source_mac, account_id, suppressed, code_mac, attempts, max_attempts, created_at, expires_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)",
			row.ID, row.Purpose, row.PhoneMAC, source, account, row.Suppressed, row.CodeMAC,
			row.MaxAttempts, clock.UTC(row.CreatedAt), clock.UTC(row.ExpiresAt)).Error; err != nil {
			return fmt.Errorf("writing a challenge: %w", err)
		}
		return nil
	})
}

// lock takes the ordered set of transaction-scoped advisory locks a send is
// serialized under: the global budget of its purpose, then the number, then the
// source address when the send carries one, then the account. Every send takes
// them in this order, so two sends that share any key cannot deadlock, and
// holding all of them is what lets the budget check and the write be one
// decision. The keys are namespaced strings hashed by the database itself; the
// locks are released when the transaction commits or rolls back.
//
// A hash collision between two different keys only over-serializes unrelated
// sends, which is safe; it never lets two sends of one key run at once. The locks
// are PostgreSQL-only: SQLite serializes writers at the database itself, so an
// overlapping second write waits on its own and there is no lock to take.
func (g *register) lock(tx *gorm.DB, row challengeRow) error {
	if g.dialect != db.Postgres {
		return nil
	}
	keys := []string{
		"authn.sms:lock:global:" + row.Purpose,
		"authn.sms:lock:phone:" + row.PhoneMAC,
	}
	if row.SourceMAC != nil {
		keys = append(keys, "authn.sms:lock:source:"+*row.SourceMAC)
	}
	if row.AccountID != nil {
		keys = append(keys, "authn.sms:lock:account:"+*row.AccountID)
	}
	for _, key := range keys {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error; err != nil {
			return fmt.Errorf("taking the send lock %q: %w", key, err)
		}
	}
	return nil
}

// increment counts one attempt on the row, conditionally: the row has to be
// there, unconsumed, unexpired and below its attempt limit. The affected-row
// count says whether this call did, which is how a concurrent submitter cannot
// get past the limit.
func (g *register) increment(ctx context.Context, id string, now time.Time) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+challengeTable+" SET attempts = attempts + 1 "+
		"WHERE id = ? AND consumed_at IS NULL AND expires_at > ? AND attempts < max_attempts",
		id, clock.UTC(now))
	if res.Error != nil {
		return false, fmt.Errorf("counting an attempt: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// read reads one row by its identifier.
func (g *register) read(ctx context.Context, id string) (challengeRow, bool, error) {
	var rows []challengeRow
	err := g.session(ctx).Table(challengeTable).
		Select("id, purpose, phone_mac, account_id, suppressed, code_mac, attempts, max_attempts, created_at, expires_at, consumed_at").
		Where("id = ?", id).Limit(1).Scan(&rows).Error
	if err != nil {
		return challengeRow{}, false, fmt.Errorf("reading a challenge: %w", err)
	}
	if len(rows) == 0 {
		return challengeRow{}, false, nil
	}
	row := rows[0]
	row.CreatedAt = clock.UTC(row.CreatedAt)
	row.ExpiresAt = clock.UTC(row.ExpiresAt)
	if row.ConsumedAt != nil {
		consumed := clock.UTC(*row.ConsumedAt)
		row.ConsumedAt = &consumed
	}
	return row, true, nil
}

// consume marks the row consumed, conditionally on the column still being empty.
// The affected-row count says whether this call did, which is how a second
// correct submit of the same challenge fails.
func (g *register) consume(ctx context.Context, id string, now time.Time) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+challengeTable+" SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL",
		clock.UTC(now), id)
	if res.Error != nil {
		return false, fmt.Errorf("consuming a challenge: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// lastCreatedForPhone reads the most recent row's created_at for the number, of
// any purpose, which the resend interval is measured from. It reads through the
// handle it is given, so the budget check can read on the send's own
// transaction.
func (g *register) lastCreatedForPhone(q *gorm.DB, phoneMAC string) (time.Time, bool, error) {
	var out []struct{ CreatedAt time.Time }
	err := q.Table(challengeTable).Select("created_at").
		Where("phone_mac = ?", phoneMAC).Order("created_at DESC").Limit(1).Scan(&out).Error
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading the last send to a number: %w", err)
	}
	if len(out) == 0 {
		return time.Time{}, false, nil
	}
	return clock.UTC(out[0].CreatedAt), true, nil
}

// countPhoneSince counts the rows for the number created after since, of any
// purpose.
func (g *register) countPhoneSince(q *gorm.DB, phoneMAC string, since time.Time) (int64, error) {
	var n int64
	err := q.Table(challengeTable).
		Where("phone_mac = ? AND created_at > ?", phoneMAC, clock.UTC(since)).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("counting the sends to a number: %w", err)
	}
	return n, nil
}

// oldestPhoneSince reads the created_at of the oldest row for the number after
// since, which is when the counting window frees a slot.
func (g *register) oldestPhoneSince(q *gorm.DB, phoneMAC string, since time.Time) (time.Time, bool, error) {
	var out []struct{ CreatedAt time.Time }
	err := q.Table(challengeTable).Select("created_at").
		Where("phone_mac = ? AND created_at > ?", phoneMAC, clock.UTC(since)).
		Order("created_at ASC").Limit(1).Scan(&out).Error
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading the oldest send to a number: %w", err)
	}
	if len(out) == 0 {
		return time.Time{}, false, nil
	}
	return clock.UTC(out[0].CreatedAt), true, nil
}

// countSourceSince counts the rows from one source address created after since,
// of any purpose. Rows with no source address are not counted: an empty source
// is not a source, and putting them all under one key would make the budget
// fail-closed on a deployment that cannot resolve an address.
func (g *register) countSourceSince(q *gorm.DB, sourceMAC string, since time.Time) (int64, error) {
	var n int64
	err := q.Table(challengeTable).
		Where("source_mac = ? AND created_at > ?", sourceMAC, clock.UTC(since)).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("counting the sends from a source: %w", err)
	}
	return n, nil
}

// oldestSourceSince reads the created_at of the oldest row from the source after
// since, which is when the counting window frees a slot.
func (g *register) oldestSourceSince(q *gorm.DB, sourceMAC string, since time.Time) (time.Time, bool, error) {
	var out []struct{ CreatedAt time.Time }
	err := q.Table(challengeTable).Select("created_at").
		Where("source_mac = ? AND created_at > ?", sourceMAC, clock.UTC(since)).
		Order("created_at ASC").Limit(1).Scan(&out).Error
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading the oldest send from a source: %w", err)
	}
	if len(out) == 0 {
		return time.Time{}, false, nil
	}
	return clock.UTC(out[0].CreatedAt), true, nil
}

// countRealPurposeSince counts the rows of one purpose created after since that
// are not suppressed. Only the deployment-wide budget reads it: a suppressed row
// is a request for a number nobody registered, and letting it spend the global
// budget would let anonymous traffic exhaust the deployment without sending
// anything. The per-number, per-source and per-account dimensions still count
// suppressed rows, so the point at which a caller is refused does not say
// whether the number is registered.
func (g *register) countRealPurposeSince(q *gorm.DB, purpose string, since time.Time) (int64, error) {
	var n int64
	err := q.Table(challengeTable).
		Where("purpose = ? AND suppressed = ? AND created_at > ?", purpose, false, clock.UTC(since)).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("counting the sends of a purpose: %w", err)
	}
	return n, nil
}

// oldestRealPurposeSince reads the created_at of the oldest unsuppressed row of
// the purpose after since, which is when the deployment-wide window frees a
// slot.
func (g *register) oldestRealPurposeSince(q *gorm.DB, purpose string, since time.Time) (time.Time, bool, error) {
	var out []struct{ CreatedAt time.Time }
	err := q.Table(challengeTable).Select("created_at").
		Where("purpose = ? AND suppressed = ? AND created_at > ?", purpose, false, clock.UTC(since)).
		Order("created_at ASC").Limit(1).Scan(&out).Error
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading the oldest send of a purpose: %w", err)
	}
	if len(out) == 0 {
		return time.Time{}, false, nil
	}
	return clock.UTC(out[0].CreatedAt), true, nil
}

// countAccountSince counts the rows for the account created after since.
func (g *register) countAccountSince(q *gorm.DB, account string, since time.Time) (int64, error) {
	var n int64
	err := q.Table(challengeTable).
		Where("account_id = ? AND created_at > ?", account, clock.UTC(since)).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("counting the sends to an account: %w", err)
	}
	return n, nil
}

// oldestAccountSince reads the created_at of the oldest row for the account
// after since.
func (g *register) oldestAccountSince(q *gorm.DB, account string, since time.Time) (time.Time, bool, error) {
	var out []struct{ CreatedAt time.Time }
	err := q.Table(challengeTable).Select("created_at").
		Where("account_id = ? AND created_at > ?", account, clock.UTC(since)).
		Order("created_at ASC").Limit(1).Scan(&out).Error
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading the oldest send to an account: %w", err)
	}
	if len(out) == 0 {
		return time.Time{}, false, nil
	}
	return clock.UTC(out[0].CreatedAt), true, nil
}

// deleteAccount removes the account's mfa and enroll rows and returns how many
// it removed. A call that finds none removes none and is not an error, which is
// what makes the cleanup idempotent.
func (g *register) deleteAccount(ctx context.Context, account string) (int64, error) {
	res := g.session(ctx).Exec("DELETE FROM "+challengeTable+
		" WHERE account_id = ? AND purpose IN (?, ?)", account, authn.SMSPurposeMFA, authn.SMSPurposeEnroll)
	if res.Error != nil {
		return 0, fmt.Errorf("deleting the challenges of an account: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// removeExpired removes one batch of rows whose expires_at is before the cutoff
// and returns how many it removed. The subquery and the delete are one
// statement, and the delete names the rows by identifier, so two replicas
// sweeping at once do not remove one row twice.
func (g *register) removeExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	res := g.session(ctx).Exec("DELETE FROM "+challengeTable+
		" WHERE id IN (SELECT id FROM "+challengeTable+" WHERE expires_at < ? LIMIT ?)",
		clock.UTC(cutoff), sweepBatch)
	if res.Error != nil {
		return 0, fmt.Errorf("removing the expired challenges: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// probe reports whether the challenge table can be read. A table that is not
// there is ErrNotMigrated, and any other failure is returned as it is.
func (g *register) probe(ctx context.Context) error {
	err := g.session(ctx).Exec("SELECT 1 FROM " + challengeTable + " WHERE 1 = 0").Error
	if err == nil {
		return nil
	}
	if !g.session(ctx).Migrator().HasTable(challengeTable) {
		return fmt.Errorf("%w: %s is not there: %w", authn.ErrNotMigrated, challengeTable, err)
	}
	return fmt.Errorf("reading %s: %w", challengeTable, err)
}
