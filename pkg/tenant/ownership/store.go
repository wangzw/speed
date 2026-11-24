package ownership

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

const transferTable = "tenant_owner_transfers"

const transferRequestTTL = 7 * 24 * time.Hour

const (
	statusPending   int16 = 0
	statusConfirmed int16 = 1
	statusDeclined  int16 = 2
	statusCancelled int16 = 3
	statusExpired   int16 = 4
)

// transferRow is deliberately not tenant-scoped: the target's confirmation has no
// tenant context.
type transferRow struct {
	ID          string    `gorm:"column:id;primaryKey"`
	TenantID    string    `gorm:"column:tenant_id"`
	FromAccount string    `gorm:"column:from_account"`
	ToAccount   string    `gorm:"column:to_account"`
	Status      int16     `gorm:"column:status"`
	RequestedAt time.Time `gorm:"column:requested_at"`
	ExpiresAt   time.Time `gorm:"column:expires_at"`
	ResolvedAt  *time.Time
}

// TableName implements gorm's namer.
func (transferRow) TableName() string { return transferTable }

// statusOf reports a value outside the closed set as expired, so a caller never
// reads an unknown value as pending.
func statusOf(value int16) tenant.TransferStatus {
	switch value {
	case statusPending:
		return tenant.TransferPending
	case statusConfirmed:
		return tenant.TransferConfirmed
	case statusDeclined:
		return tenant.TransferDeclined
	case statusCancelled:
		return tenant.TransferCancelled
	default:
		return tenant.TransferExpired
	}
}

func requestOf(row transferRow) tenant.TransferRequest {
	return tenant.TransferRequest{
		ID:          row.ID,
		Tenant:      tenant.ID(row.TenantID),
		From:        row.FromAccount,
		To:          row.ToAccount,
		RequestedAt: row.RequestedAt.UTC(),
		ExpiresAt:   row.ExpiresAt.UTC(),
		Status:      statusOf(row.Status),
		ResolvedAt:  utcPointer(row.ResolvedAt),
	}
}

func utcPointer(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := in.UTC()
	return &out
}

func (p *product) getRequest(ctx context.Context, handle *gorm.DB, requestID string) (transferRow, bool, error) {
	var row transferRow
	err := handle.WithContext(ctx).Where("id = ?", requestID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return transferRow{}, false, nil
	}
	if err != nil {
		return transferRow{}, false, fmt.Errorf("%s: reading the owner-transfer request: %w", ModuleName, err)
	}
	return row, true, nil
}

func (p *product) settleExpiredRequest(ctx context.Context, requestID string, now time.Time) error {
	return p.settleExpiredWhere(ctx, "id = ?", requestID, now)
}

func (p *product) settleExpiredForTenant(ctx context.Context, id tenant.ID, now time.Time) error {
	return p.settleExpiredWhere(ctx, "tenant_id = ?", string(id), now)
}

func (p *product) settleExpiredForTarget(ctx context.Context, target string, now time.Time) error {
	return p.settleExpiredWhere(ctx, "to_account = ?", target, now)
}

func (p *product) settleExpiredWhere(ctx context.Context, where string, value any, now time.Time) error {
	err := p.database.DB().WithContext(ctx).Model(&transferRow{}).
		Where("status = ? AND expires_at <= ?", statusPending, now.UTC()).
		Where(where, value).
		Updates(map[string]any{"status": statusExpired, "resolved_at": now.UTC()}).Error
	if err != nil {
		return fmt.Errorf("%s: settling expired owner-transfer requests: %w", ModuleName, err)
	}
	return nil
}

func (p *product) pendingRowForTenant(ctx context.Context, handle *gorm.DB, id tenant.ID) (transferRow, bool, error) {
	var rows []transferRow
	err := handle.WithContext(ctx).
		Where("tenant_id = ? AND status = ?", string(id), statusPending).
		Limit(1).
		Find(&rows).Error
	if err != nil {
		return transferRow{}, false, fmt.Errorf("%s: reading the pending owner-transfer request: %w", ModuleName, err)
	}
	if len(rows) == 0 {
		return transferRow{}, false, nil
	}
	return rows[0], true, nil
}

func (p *product) pendingRowsForTarget(ctx context.Context, target string) ([]transferRow, error) {
	var rows []transferRow
	err := p.database.DB().WithContext(ctx).
		Where("to_account = ? AND status = ?", target, statusPending).
		Order("requested_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("%s: reading the target's pending owner-transfer requests: %w", ModuleName, err)
	}
	return rows, nil
}

func resolveRequest(ctx context.Context, handle *gorm.DB, requestID string, status int16, now time.Time) (bool, error) {
	written := handle.WithContext(ctx).Model(&transferRow{}).
		Where("id = ? AND status = ?", requestID, statusPending).
		Updates(map[string]any{"status": status, "resolved_at": now.UTC()})
	if written.Error != nil {
		return false, written.Error
	}
	return written.RowsAffected == 1, nil
}

// isUniqueViolation: the pending index is the only unique constraint besides the
// key, so a collision is the second-request refusal.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") ||
		strings.Contains(message, "duplicate key value")
}
