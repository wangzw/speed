package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// table is the ledger's one table.
const table = "sms_budget_events"

// packageName prefixes every error this package builds, so a failure is
// attributable when it travels through a caller that adds no context of its own.
const packageName = "sms.budget"

var (
	// ErrInvalidAttempt reports an attempt that is not a row this ledger can
	// write: an unknown channel or outcome, an empty purpose or destination, a
	// source or account that is present and empty, a zero attempted time, a
	// refund before the attempt, or a suppressed attempt that names no refund.
	ErrInvalidAttempt = errors.New("sms.budget: the attempt is not valid")
	// ErrInvalidQuery reports a query this ledger cannot compile: an unknown
	// dimension or channel, a value with no dimension or a dimension with no
	// value, a zero since, OnlyRefunded together with IncludeRefunded, or a
	// prune with a zero cutoff.
	ErrInvalidQuery = errors.New("sms.budget: the query is not valid")
	// ErrNotFound reports a refund naming an attempt this ledger holds no row
	// for.
	ErrNotFound = errors.New("sms.budget: no attempt with that identifier")
)

// Store is the ledger's calling surface.
//
// A Store is safe for concurrent use. Record is one INSERT and every read is a
// query over the table; the implementation keeps no counter and no in-process
// state, so two goroutines recording at once cannot lose one of the rows they
// wrote.
//
// The store locks nothing. A caller whose budget has to be a hard cap runs its
// counts and its Record in one transaction, on a Store built over that
// transaction with New, and serialises the dimension itself. Two callers that
// do not share a transaction do not see each other's uncommitted rows between
// the count and the insert, which is the ordinary shape of a soft cap.
type Store interface {
	// Record writes one attempt and answers its identifier. It refuses a
	// malformed attempt with ErrInvalidAttempt and writes nothing. A refunded
	// attempt may be written directly, which is how a message that never
	// reached the carrier is recorded as an attempt that spends no send
	// budget.
	Record(ctx context.Context, a Attempt) (ID, error)
	// MarkRefunded marks one attempt refunded at at, which removes it from
	// every send count and leaves it in every attempt count. It is idempotent:
	// a second call leaves the first refund time in place and reports no
	// error. An identifier with no row is ErrNotFound.
	MarkRefunded(ctx context.Context, id ID, at time.Time) error
	// Count answers how many attempts match q. The refund and suppression
	// switches in q decide which rows of the filters and window count; the Query
	// documentation states the exact predicates.
	Count(ctx context.Context, q Query) (int64, error)
	// CountDistinctDestinations answers how many different destination hashes
	// match q. Two attempts to one destination count once, so a source that
	// changes the number it targets is measured by the numbers rather than by
	// the calls. IncludeRefunded decides whether a refunded attempt's
	// destination stays in the count, as it does for Count.
	CountDistinctDestinations(ctx context.Context, q Query) (int64, error)
	// Oldest answers the attempted time of the oldest attempt matching q,
	// which is when the window frees a slot; the boolean is false when no row
	// matches. It reads the same rows Count does for the same q.
	Oldest(ctx context.Context, q Query) (time.Time, bool, error)
	// Latest answers the attempted time of the newest attempt matching q,
	// which is what a resend interval is measured from; the boolean is false
	// when no row matches. It reads the same rows Count does for the same q.
	Latest(ctx context.Context, q Query) (time.Time, bool, error)
	// CountCeiling answers how many non-refunded, non-suppressed attempts both
	// channels wrote since since. It is the cross-channel emergency cap: one
	// count over both channels, and neither a refunded nor a suppressed attempt
	// spends it.
	CountCeiling(ctx context.Context, since time.Time) (int64, error)
	// PruneBefore deletes every row attempted strictly before before and
	// answers how many it deleted. It exists for a periodic sweep of the
	// retention window; it reads no budget and changes no count outside the
	// deleted window. A zero cutoff is ErrInvalidQuery.
	PruneBefore(ctx context.Context, before time.Time) (int64, error)
}

// New returns the ledger over handle, which must not be nil. The module
// delivers one over the database's handle; a caller that must run a count and a
// record in one transaction builds another over that transaction with New.
func New(handle *gorm.DB) Store {
	return &sqlStore{handle: handle}
}

// sqlStore is the SQL implementation of Store.
type sqlStore struct {
	handle *gorm.DB
}

var _ Store = (*sqlStore)(nil)

// row is the table's shape. The column tags are explicit because the Go names
// carry acronyms, and a mapping that is inferred is a mapping that can change
// under a rename.
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

// TableName fixes the table the ledger reads and writes.
func (row) TableName() string { return table }

// Record writes one attempt.
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

// MarkRefunded marks one attempt refunded.
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
	// Nothing was updated: either the row is already refunded, which is the
	// idempotent case, or there is no such row. The second read tells them
	// apart, and the first refund time is never overwritten.
	var held int64
	if err := s.handle.WithContext(ctx).Model(&row{}).Where("id = ?", int64(id)).Count(&held).Error; err != nil {
		return fmt.Errorf("%s: reading an attempt before refunding it: %w", packageName, err)
	}
	if held == 0 {
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return nil
}

// Count counts the attempts q selects.
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

// CountDistinctDestinations counts the different destinations q selects.
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

// Oldest reads the attempted time of the oldest attempt q selects.
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

// Latest reads the attempted time of the newest attempt q selects.
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

// CountCeiling counts the non-refunded, non-suppressed attempts of both
// channels since since.
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

// PruneBefore deletes every row attempted strictly before before.
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

// scope compiles q into the WHERE clause every read uses. Model is set here, so
// a caller of scope only adds the select shape it needs.
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
		// Rows refunded after the fact. IncludeSuppressed still decides
		// whether the suppressed ones, which are refunded at write time,
		// take part.
		db = db.Where("refunded_at IS NOT NULL")
		if !q.IncludeSuppressed {
			db = db.Where("suppressed = ?", false)
		}
	case q.IncludeRefunded:
		// Every attempt, refunded or not.
		if !q.IncludeSuppressed {
			db = db.Where("suppressed = ?", false)
		}
	case q.IncludeSuppressed:
		// A send budget, with the suppressed rows counted as charges: they
		// are refunded at write time, so the refund condition alone would
		// leave them out.
		db = db.Where("(refunded_at IS NULL OR suppressed = ?)", true)
	default:
		db = db.Where("refunded_at IS NULL AND suppressed = ?", false)
	}
	db = db.Where("attempted_at >= ?", q.Since.UTC())
	return db, nil
}

// dimensionColumn names the column a dimension ranks. The switch is the only
// place a Dimension becomes SQL, so a value outside the closed set cannot reach
// a statement.
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

// validate holds an attempt to what the table can carry.
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

// validate holds a query to what scope can compile.
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

// utcPointer normalizes a nullable time the way every written time is
// normalized, without turning a nil into a value.
func utcPointer(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}
