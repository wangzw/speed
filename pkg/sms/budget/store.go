package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const table = "sms_budget_events"

const packageName = "sms.budget"

var (
	// ErrInvalidAttempt reports an attempt this ledger cannot write: an unknown
	// channel or outcome, an empty purpose or destination, an empty present source or
	// account, a zero attempted time, a refund before it, or a suppressed attempt with no refund.
	ErrInvalidAttempt = errors.New("sms.budget: the attempt is not valid")
	// ErrInvalidQuery reports a query this ledger cannot compile: an unknown dimension
	// or channel, a value without a dimension or a dimension without a value, a zero
	// since, both refund switches, or a prune with a zero cutoff.
	ErrInvalidQuery = errors.New("sms.budget: the query is not valid")
	// ErrNotFound reports a refund naming an attempt this ledger holds no row for.
	ErrNotFound = errors.New("sms.budget: no attempt with that identifier")
)

// Store is the ledger calling surface, safe for concurrent use. The store keeps no
// counter and locks nothing; a hard cap is the caller's transaction.
type Store interface {
	// Record writes one attempt and answers its identifier, refusing a malformed
	// attempt with ErrInvalidAttempt and writing nothing. A refunded attempt may be
	// written directly.
	Record(ctx context.Context, a Attempt) (ID, error)
	// MarkRefunded marks one attempt refunded at at, which leaves every send count and
	// stays in every attempt count. It is idempotent; an identifier with no row is ErrNotFound.
	MarkRefunded(ctx context.Context, id ID, at time.Time) error
	// Count answers how many attempts match q; Query states how the switches select rows.
	Count(ctx context.Context, q Query) (int64, error)
	// CountDistinctDestinations answers how many different destination hashes match q;
	// IncludeRefunded decides whether a refunded destination stays in the count.
	CountDistinctDestinations(ctx context.Context, q Query) (int64, error)
	// Oldest answers the attempted time of the oldest attempt matching q, false when no
	// row matches.
	Oldest(ctx context.Context, q Query) (time.Time, bool, error)
	// Latest answers the attempted time of the newest attempt matching q, false when no
	// row matches.
	Latest(ctx context.Context, q Query) (time.Time, bool, error)
	// CountCeiling answers how many non-refunded, non-suppressed attempts both channels
	// wrote since since.
	CountCeiling(ctx context.Context, since time.Time) (int64, error)
	// PruneBefore deletes every row attempted strictly before before and answers how
	// many it deleted; a zero cutoff is ErrInvalidQuery.
	PruneBefore(ctx context.Context, before time.Time) (int64, error)
}

// New returns the ledger over handle, which must not be nil. A caller that needs a
// count and a record in one transaction builds another over that transaction.
func New(handle *gorm.DB) Store {
	return &sqlStore{handle: handle}
}

type sqlStore struct {
	handle *gorm.DB
}

var _ Store = (*sqlStore)(nil)

type row struct {
	ID             int64      `gorm:"column:id;primaryKey;autoIncrement"`
	Channel        string     `gorm:"column:channel"`
	Purpose        string     `gorm:"column:purpose"`
	SourceMAC      *string    `gorm:"column:source_mac"`
	DestinationMAC string     `gorm:"column:destination_mac"`
	TenantID       string     `gorm:"column:tenant_id"`
	AccountID      *string    `gorm:"column:account_id"`
	AttemptedAt    time.Time  `gorm:"column:attempted_at"`
	Outcome        string     `gorm:"column:outcome"`
	RefundedAt     *time.Time `gorm:"column:refunded_at"`
	Suppressed     bool       `gorm:"column:suppressed"`
}

func (row) TableName() string { return table }

func (s *sqlStore) Record(ctx context.Context, a Attempt) (ID, error) {
	if err := a.validate(); err != nil {
		return 0, err
	}
	write := row{
		Channel:        string(a.Channel),
		Purpose:        a.Purpose,
		SourceMAC:      a.SourceMAC,
		DestinationMAC: a.DestinationMAC,
		TenantID:       a.TenantID,
		AccountID:      a.AccountID,
		AttemptedAt:    a.AttemptedAt.UTC(),
		Outcome:        string(a.Outcome),
		RefundedAt:     utcPointer(a.RefundedAt),
		Suppressed:     a.Suppressed,
	}
	if err := s.handle.WithContext(ctx).Create(&write).Error; err != nil {
		return 0, fmt.Errorf("%s: recording an attempt: %w", packageName, err)
	}
	return ID(write.ID), nil
}

func (s *sqlStore) MarkRefunded(ctx context.Context, id ID, at time.Time) error {
	if id <= 0 {
		return fmt.Errorf("%w: the identifier %d names no attempt", ErrNotFound, id)
	}
	if at.IsZero() {
		return fmt.Errorf("%w: the refund names no time", ErrInvalidAttempt)
	}
	result := s.handle.WithContext(ctx).Model(&row{}).
		Where("id = ? AND refunded_at IS NULL", int64(id)).
		Update("refunded_at", at.UTC())
	if result.Error != nil {
		return fmt.Errorf("%s: refunding an attempt: %w", packageName, result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var held int64
	if err := s.handle.WithContext(ctx).Model(&row{}).Where("id = ?", int64(id)).Count(&held).Error; err != nil {
		return fmt.Errorf("%s: reading an attempt before refunding it: %w", packageName, err)
	}
	if held == 0 {
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return nil
}

func (s *sqlStore) Count(ctx context.Context, q Query) (int64, error) {
	scope, err := s.scope(ctx, q)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := scope.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("%s: counting attempts: %w", packageName, err)
	}
	return n, nil
}

func (s *sqlStore) CountDistinctDestinations(ctx context.Context, q Query) (int64, error) {
	scope, err := s.scope(ctx, q)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := scope.Distinct("destination_mac").Count(&n).Error; err != nil {
		return 0, fmt.Errorf("%s: counting distinct destinations: %w", packageName, err)
	}
	return n, nil
}

func (s *sqlStore) Oldest(ctx context.Context, q Query) (time.Time, bool, error) {
	scope, err := s.scope(ctx, q)
	if err != nil {
		return time.Time{}, false, err
	}
	var out []struct{ AttemptedAt time.Time }
	if err := scope.Select("attempted_at").Order("attempted_at ASC").Limit(1).Scan(&out).Error; err != nil {
		return time.Time{}, false, fmt.Errorf("%s: reading the oldest attempt: %w", packageName, err)
	}
	if len(out) == 0 {
		return time.Time{}, false, nil
	}
	return out[0].AttemptedAt.UTC(), true, nil
}

func (s *sqlStore) Latest(ctx context.Context, q Query) (time.Time, bool, error) {
	scope, err := s.scope(ctx, q)
	if err != nil {
		return time.Time{}, false, err
	}
	var out []struct{ AttemptedAt time.Time }
	if err := scope.Select("attempted_at").Order("attempted_at DESC").Limit(1).Scan(&out).Error; err != nil {
		return time.Time{}, false, fmt.Errorf("%s: reading the newest attempt: %w", packageName, err)
	}
	if len(out) == 0 {
		return time.Time{}, false, nil
	}
	return out[0].AttemptedAt.UTC(), true, nil
}

func (s *sqlStore) CountCeiling(ctx context.Context, since time.Time) (int64, error) {
	if since.IsZero() {
		return 0, fmt.Errorf("%w: the ceiling's window starts at no time", ErrInvalidQuery)
	}
	var n int64
	err := s.handle.WithContext(ctx).Model(&row{}).
		Where("channel IN ?", []string{string(ChannelAuthn), string(ChannelNotification)}).
		Where("attempted_at >= ? AND refunded_at IS NULL AND suppressed = ?", since.UTC(), false).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("%s: counting the cross-channel ceiling: %w", packageName, err)
	}
	return n, nil
}

func (s *sqlStore) PruneBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, fmt.Errorf("%w: the prune names no cutoff", ErrInvalidQuery)
	}
	result := s.handle.WithContext(ctx).Where("attempted_at < ?", before.UTC()).Delete(&row{})
	if result.Error != nil {
		return 0, fmt.Errorf("%s: pruning attempts: %w", packageName, result.Error)
	}
	return result.RowsAffected, nil
}

func (s *sqlStore) scope(ctx context.Context, q Query) (*gorm.DB, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	db := s.handle.WithContext(ctx).Model(&row{})
	if q.Channel != "" {
		db = db.Where("channel = ?", string(q.Channel))
	}
	if q.Purpose != "" {
		db = db.Where("purpose = ?", q.Purpose)
	}
	if q.Dimension != "" {
		db = db.Where(dimensionColumn(q.Dimension)+" = ?", q.Value)
	}
	switch {
	case q.OnlyRefunded:
		db = db.Where("refunded_at IS NOT NULL")
		if !q.IncludeSuppressed {
			db = db.Where("suppressed = ?", false)
		}
	case q.IncludeRefunded:
		if !q.IncludeSuppressed {
			db = db.Where("suppressed = ?", false)
		}
	case q.IncludeSuppressed:
		db = db.Where("(refunded_at IS NULL OR suppressed = ?)", true)
	default:
		db = db.Where("refunded_at IS NULL AND suppressed = ?", false)
	}
	db = db.Where("attempted_at >= ?", q.Since.UTC())
	return db, nil
}

func dimensionColumn(d Dimension) string {
	switch d {
	case DimensionSource:
		return "source_mac"
	case DimensionDestination:
		return "destination_mac"
	case DimensionTenant:
		return "tenant_id"
	case DimensionAccount:
		return "account_id"
	default:
		return ""
	}
}

func (a Attempt) validate() error {
	if !a.Channel.Valid() {
		return fmt.Errorf("%w: the channel %q is not one of the two this ledger records", ErrInvalidAttempt, a.Channel)
	}
	if strings.TrimSpace(a.Purpose) == "" {
		return fmt.Errorf("%w: the attempt names no purpose", ErrInvalidAttempt)
	}
	if a.SourceMAC != nil && strings.TrimSpace(*a.SourceMAC) == "" {
		return fmt.Errorf("%w: the source is present and empty; a sourceless attempt names none", ErrInvalidAttempt)
	}
	if strings.TrimSpace(a.DestinationMAC) == "" {
		return fmt.Errorf("%w: the attempt names no destination", ErrInvalidAttempt)
	}
	if a.AccountID != nil && strings.TrimSpace(*a.AccountID) == "" {
		return fmt.Errorf("%w: the account is present and empty; an accountless attempt names none", ErrInvalidAttempt)
	}
	if a.AttemptedAt.IsZero() {
		return fmt.Errorf("%w: the attempt names no time", ErrInvalidAttempt)
	}
	if !a.Outcome.Valid() {
		return fmt.Errorf("%w: the outcome %q is not one this ledger records", ErrInvalidAttempt, a.Outcome)
	}
	if a.RefundedAt != nil && a.RefundedAt.Before(a.AttemptedAt) {
		return fmt.Errorf("%w: the refund precedes the attempt", ErrInvalidAttempt)
	}
	if a.Suppressed && a.RefundedAt == nil {
		return fmt.Errorf("%w: a suppressed attempt is refunded at write time, and this one names no "+
			"refund", ErrInvalidAttempt)
	}
	return nil
}

func (q Query) validate() error {
	if q.Since.IsZero() {
		return fmt.Errorf("%w: the window starts at no time", ErrInvalidQuery)
	}
	if q.Channel != "" && !q.Channel.Valid() {
		return fmt.Errorf("%w: the channel %q is not one of the two this ledger records", ErrInvalidQuery, q.Channel)
	}
	if q.OnlyRefunded && q.IncludeRefunded {
		return fmt.Errorf("%w: OnlyRefunded asks for the refunded rows and IncludeRefunded for every "+
			"row; name one", ErrInvalidQuery)
	}
	if q.Dimension == "" {
		if q.Value != "" {
			return fmt.Errorf("%w: a value %q names no dimension", ErrInvalidQuery, q.Value)
		}
		return nil
	}
	if !q.Dimension.Valid() {
		return fmt.Errorf("%w: the dimension %q is not an indexed column", ErrInvalidQuery, q.Dimension)
	}
	if q.Value == "" {
		return fmt.Errorf("%w: the dimension %q names no value", ErrInvalidQuery, q.Dimension)
	}
	return nil
}

func utcPointer(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}
