package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/tenant"
)

// The closed set of ledger statuses. A row is exactly one of these: a channel a
// carrier accepted, a channel that was available and whose send failed
// terminally, and a channel whose seam is not in this assembly. A carrier's
// receipt is acceptance, not delivery — a vendor with asynchronous receipts
// answers only with a message identifier, and the final state is not on this
// ledger. The set matches the CHECK the migration declares.
const (
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusSkipped   = "skipped"
)

// tableName is the ledger table. It is written here rather than derived from the
// model because the name also appears in each dialect's migration file, and the
// three are kept in step by hand.
const tableName = "notification_sends"

// sendRow is the row shape of the ledger.
//
// It embeds the tenant convention, which is both the declaration that a row
// belongs to a tenant and the column that carries it: a statement issued through
// this model is scoped to the tenant of its context, filled with it on insert
// and judged against it on write. The key leads with that column, as every
// scoped table's does.
//
// Every instant is a count of milliseconds since the Unix epoch, matching the
// columns the migration declares.
type sendRow struct {
	tenant.Model
	ID                string  `gorm:"column:id;primaryKey"`
	TypeKey           string  `gorm:"column:type_key;not null"`
	Channel           string  `gorm:"column:channel;not null"`
	RecipientClass    string  `gorm:"column:recipient_class;not null"`
	RecipientUserID   string  `gorm:"column:recipient_user_id;not null"`
	Status            string  `gorm:"column:status;not null"`
	DurationMS        int64   `gorm:"column:duration_ms;not null"`
	Error             *string `gorm:"column:error"`
	ProviderReceiptID *string `gorm:"column:provider_receipt_id"`
	IdempotencyKey    string  `gorm:"column:idempotency_key;not null"`
	CreatedAt         int64   `gorm:"column:created_at;not null"`
	UpdatedAt         int64   `gorm:"column:updated_at;not null"`
}

// TableName is the table the ledger rows live in.
func (sendRow) TableName() string { return tableName }

// ledger reads and writes the delivery ledger.
type ledger struct {
	handle *gorm.DB
}

// lookup reads the status one idempotency key already holds, within the tenant
// of the context. The second result is false when the key has no row, which is
// the state a freshly derived key is in.
func (l *ledger) lookup(ctx context.Context, key string) (string, bool, error) {
	var row sendRow
	err := l.handle.WithContext(ctx).Where("idempotency_key = ?", key).First(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("%s: reading the ledger for a delivery: %w", ModuleName, err)
	}
	return row.Status, true, nil
}

// record lands one channel's result. It inserts the row, or updates the row the
// same key already holds: a channel that failed on one attempt and is retried
// settles into the same row rather than leaving its first failure beside the
// second. The unique index on (tenant_id, idempotency_key) is what settles a
// concurrent pair of writes into one row; the tenant column is filled from the
// context by the model's own filter, and a context carrying no tenant is refused
// with nothing written.
func (l *ledger) record(ctx context.Context, row sendRow) error {
	err := l.handle.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "idempotency_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "duration_ms", "error", "provider_receipt_id", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("%s: recording a %s delivery: %w", ModuleName, row.Channel, err)
	}
	return nil
}

// nowMS is the instant a row is stamped from.
func nowMS() int64 { return time.Now().UTC().UnixMilli() }

// textPtr is the value an optional text column takes: the text, or a null when
// it is empty.
func textPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
