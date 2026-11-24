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

const (
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusSkipped   = "skipped"
)

// tableName is the ledger table. It is written here rather than derived from the
// model because the name also appears in each dialect's migration file, and the
// three are kept in step by hand.
const tableName = "notification_sends"

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

func (sendRow) TableName() string { return tableName }

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

func nowMS() int64 { return time.Now().UTC().UnixMilli() }

func textPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
