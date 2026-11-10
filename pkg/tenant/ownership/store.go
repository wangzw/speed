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

// transferTable is the table the pending requests live in. It belongs to this
// module alone; no other module writes it.
const transferTable = "tenant_owner_transfers"

// transferRequestTTL is the fixed confirmation window. It is a constant rather
// than a configuration item: this module holds no configuration of its own, and
// a window that cannot be configured cannot be set to a value that outlives the
// lifecycle it describes.
const transferRequestTTL = 7 * 24 * time.Hour

// The stored status values. They are the column's closed set, written here once
// so the SQL predicates and the Go statuses cannot drift apart.
const (
	statusPending   int16 = 0
	statusConfirmed int16 = 1
	statusDeclined  int16 = 2
	statusCancelled int16 = 3
	statusExpired   int16 = 4
)

// transferRow is one row of tenant_owner_transfers. The table is global rather
// than tenant-scoped on purpose: the target's confirmation carries no tenant
// context, so the row cannot depend on one. Every store method takes the tenant
// or the target explicitly.
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

// statusOf maps a stored value to the exported status. A value outside the
// closed set is reported as expired: it can never be confirmed, and a caller
// that branches on the status must not read an unknown value as pending.
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

// requestOf builds the exported value from one row.
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

// utcPointer converts a stored instant to UTC without changing the row it came
// from.
func utcPointer(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := in.UTC()
	return &out
}

// getRequest reads one row by identifier through the given handle.
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

// settleExpiredRequest marks one pending row expired when its window has
// passed. The conditional write is the arbiter: a row another writer already
// resolved matches nothing, so exactly one resolution wins.
func (p *product) settleExpiredRequest(ctx context.Context, requestID string, now time.Time) error {
	return p.settleExpiredWhere(ctx, "id = ?", requestID, now)
}

// settleExpiredForTenant settles every expired request of one organization.
func (p *product) settleExpiredForTenant(ctx context.Context, id tenant.ID, now time.Time) error {
	return p.settleExpiredWhere(ctx, "tenant_id = ?", string(id), now)
}

// settleExpiredForTarget settles every expired request addressed to one
// account.
func (p *product) settleExpiredForTarget(ctx context.Context, target string, now time.Time) error {
	return p.settleExpiredWhere(ctx, "to_account = ?", target, now)
}

// settleExpiredWhere is the lazy expiry every read and action runs first. It
// needs no tenant lock: it changes no ownership, and the conditional update
// serialises with the confirm's own conditional update.
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

// pendingRowForTenant reads the organization's pending request through the
// given handle, after the caller settled expired rows.
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

// pendingRowsForTarget reads every pending request addressed to one account.
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

// resolveRequest writes one terminal state onto a pending row, through the
// given handle. It reports whether the row was pending; a row another writer
// resolved first matches nothing.
func resolveRequest(ctx context.Context, handle *gorm.DB, requestID string, status int16, now time.Time) (bool, error) {
	written := handle.WithContext(ctx).Model(&transferRow{}).
		Where("id = ? AND status = ?", requestID, statusPending).
		Updates(map[string]any{"status": status, "resolved_at": now.UTC()})
	if written.Error != nil {
		return false, written.Error
	}
	return written.RowsAffected == 1, nil
}

// isUniqueViolation reports whether a write failed because a unique index
// refused it. Both dialects this repository runs on are recognised by their
// message; the pending index is the only unique constraint the table carries
// besides its key, so a collision here is the second-request refusal.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") ||
		strings.Contains(message, "duplicate key value")
}
