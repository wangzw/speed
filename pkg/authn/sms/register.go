package sms

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/sms/budget"
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
// The shared ledger's attempt row is written in the same transaction, after the
// guard and before the challenge row: a send that a budget refused writes
// neither, and a send that is admitted is counted by the cross-channel ceiling
// from the moment it commits. The returned identifier is the ledger row's, which
// the refund paths mark refunded when the message certainly did not leave.
//
// The void, the write and, when guard is set, the guard run in one transaction
// under lock: the guard is where the budget check lives, so the counts it reads
// and the row it guards are serialized against every send that shares a
// dimension. A guard error rolls the transaction back, so a refused send writes
// no row.
func (g *register) insert(ctx context.Context, row challengeRow, attempt *budget.Attempt, guard func(*gorm.DB) error) (budget.ID, error) {
	var eventID budget.ID
	err := g.session(ctx).Transaction(func(tx *gorm.DB) error {
		if err := g.lock(tx, row); err != nil {
			return err
		}
		if guard != nil {
			if err := guard(tx); err != nil {
				return err
			}
		}
		if attempt != nil {
			id, err := budget.New(tx).Record(ctx, *attempt)
			if err != nil {
				return err
			}
			eventID = id
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
		var event any
		if eventID > 0 {
			event = int64(eventID)
		}
		if err := tx.Exec("INSERT INTO "+challengeTable+
			" (id, purpose, phone_mac, source_mac, account_id, suppressed, code_mac, attempts, max_attempts, created_at, expires_at, budget_event_id) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)",
			row.ID, row.Purpose, row.PhoneMAC, source, account, row.Suppressed, row.CodeMAC,
			row.MaxAttempts, clock.UTC(row.CreatedAt), clock.UTC(row.ExpiresAt), event).Error; err != nil {
			return fmt.Errorf("writing a challenge: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return eventID, nil
}

// lock takes the ordered set of transaction-scoped advisory locks a send is
// serialized under: the shared cross-channel ceiling first, then the global
// budget of its purpose, then the number, then the source address when the send
// carries one, then the account. Every send takes them in this order, so two
// sends that share any key cannot deadlock, and holding all of them is what lets
// the budget check and the write be one decision. The ceiling key is the one
// lock both sending channels share; it is a single deployment-wide key, so the
// authentication channel's concurrent sends are serialized against each other
// and against any other sender that takes the same key. The keys are namespaced
// strings hashed by the database itself; the locks are released when the
// transaction commits or rolls back.
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
		"sms.budget:lock:ceiling",
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

// refund gives a challenge's spend back and invalidates its code. The row is
// marked refunded, which no budget counts, and spent in the same statement, so a
// code that never reached the carrier cannot pass the check; a row another
// request already consumed keeps its own consumption time. A row that is no
// longer there — swept, or erased with its account — changes nothing and is not
// an error.
//
// It answers the challenge's ledger attempt identifier, which the caller marks
// refunded on the shared ledger so the cross-channel ceiling stops counting a
// message that certainly did not leave. A row written before the ledger was in
// force carries none, which reads as the zero identifier.
func (g *register) refund(ctx context.Context, id string, now time.Time) (budget.ID, error) {
	var out []struct{ BudgetEventID *int64 }
	if err := g.session(ctx).Table(challengeTable).Select("budget_event_id").
		Where("id = ?", id).Limit(1).Scan(&out).Error; err != nil {
		return 0, fmt.Errorf("reading a challenge's ledger attempt: %w", err)
	}
	var eventID budget.ID
	if len(out) > 0 && out[0].BudgetEventID != nil {
		eventID = budget.ID(*out[0].BudgetEventID)
	}
	res := g.session(ctx).Exec("UPDATE "+challengeTable+
		" SET refunded_at = ?, consumed_at = COALESCE(consumed_at, ?) WHERE id = ?",
		clock.UTC(now), clock.UTC(now), id)
	if res.Error != nil {
		return eventID, fmt.Errorf("refunding a challenge: %w", res.Error)
	}
	return eventID, nil
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
