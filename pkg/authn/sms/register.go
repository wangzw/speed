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

const sweepBatch = 1000

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

type register struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

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

func (g *register) increment(ctx context.Context, id string, now time.Time) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+challengeTable+" SET attempts = attempts + 1 "+
		"WHERE id = ? AND consumed_at IS NULL AND expires_at > ? AND attempts < max_attempts",
		id, clock.UTC(now))
	if res.Error != nil {
		return false, fmt.Errorf("counting an attempt: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

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

func (g *register) consume(ctx context.Context, id string, now time.Time) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+challengeTable+" SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL",
		clock.UTC(now), id)
	if res.Error != nil {
		return false, fmt.Errorf("consuming a challenge: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

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

func (g *register) deleteAccount(ctx context.Context, account string) (int64, error) {
	res := g.session(ctx).Exec("DELETE FROM "+challengeTable+
		" WHERE account_id = ? AND purpose IN (?, ?)", account, authn.SMSPurposeMFA, authn.SMSPurposeEnroll)
	if res.Error != nil {
		return 0, fmt.Errorf("deleting the challenges of an account: %w", res.Error)
	}
	return res.RowsAffected, nil
}

func (g *register) removeExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	res := g.session(ctx).Exec("DELETE FROM "+challengeTable+
		" WHERE id IN (SELECT id FROM "+challengeTable+" WHERE expires_at < ? LIMIT ?)",
		clock.UTC(cutoff), sweepBatch)
	if res.Error != nil {
		return 0, fmt.Errorf("removing the expired challenges: %w", res.Error)
	}
	return res.RowsAffected, nil
}

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
