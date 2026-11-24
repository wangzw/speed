package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// ErrInvalidQuery reports a query this read face cannot read: a cursor
// that is not one it handed out.
var ErrInvalidQuery = errors.New("audit: the query is not one this read face can read")

// ErrReadFailed reports that the audit table could not be read.
var ErrReadFailed = errors.New("audit: the audit table could not be read")

// DefaultPageSize is the page size a query that asks for none gets.
const DefaultPageSize = 50

// MaxPageSize is the largest page a query can ask for. A query that asks for
// more is read at this size.
const MaxPageSize = 200

// Reader is the read surface of the audit table, delivered by this module
// beside the capture paths.
type Reader interface {
	// Query reads one page of audit rows, newest first, returning the page
	// and the cursor of the next, which is empty on the last page.
	Query(ctx context.Context, q Query) ([]Row, string, error)
}

// Query narrows one read.
type Query struct {
	// ID, when non-zero, reads that one row. The row identifier is unique over
	// the whole table, so this needs no tenant.
	ID uint64
	// Since is the lower time bound, inclusive. The zero time means no lower
	// bound.
	Since time.Time
	// Until is the upper time bound, exclusive. The zero time means no upper
	// bound.
	Until time.Time
	// Actions, when non-empty, reads only the rows whose action is one of these.
	Actions []string
	// Outcomes, when non-empty, reads only the rows whose outcome is one of
	// these.
	Outcomes []string
	// Actors, when non-empty, reads only the rows whose actor is one of these.
	// An actor is the acting identity's key; a row with no actor holds the empty
	// string, so an item that is the empty string selects those rows.
	Actors []string
	// After is the previous page's cursor. Empty starts at the newest row.
	After string
	// Limit is the page size. A non-positive value takes DefaultPageSize; a
	// value above MaxPageSize is read at MaxPageSize.
	Limit int
}

// Row is one row of the audit table, read by column.
type Row struct {
	// ID is the row identifier, unique over the whole table.
	ID uint64
	// OccurredAt is when the row's fact happened, in UTC.
	OccurredAt time.Time
	// Actor is the acting identity's key, or empty for a system operation.
	Actor string
	// Tenant is the row's tenant, or empty when the row belongs to no tenant: a
	// platform operation or a system-level one. The writer never puts an empty
	// string here, so empty says exactly "no tenant".
	Tenant string
	// Source names the capture path the row came through.
	Source string
	// Action is what was done.
	Action string
	// Object is the type of thing it was done to, with its identifier.
	Object string
	// Outcome is how it ended.
	Outcome string
	// Purpose is why it was done.
	Purpose string
	// Detail is the row's key-value detail.
	Detail map[string]string
}

type readModel struct {
	ID         uint64    `gorm:"column:id;primaryKey"`
	OccurredAt time.Time `gorm:"column:occurred_at"`
	Actor      string    `gorm:"column:actor"`
	Source     string    `gorm:"column:source"`
	Action     string    `gorm:"column:action"`
	Object     string    `gorm:"column:object"`
	Outcome    string    `gorm:"column:outcome"`
	Purpose    string    `gorm:"column:purpose"`
	Detail     string    `gorm:"column:detail"`

	tenant.Model

	// TenantID is the convention's scoping column; a NULL reads as nil, meaning
	// no tenant.
	TenantID *string `gorm:"column:tenant_id"`
}

func (readModel) TableName() string { return tableName }

func (j *journal) Query(ctx context.Context, q Query) ([]Row, string, error) {
	limit := pageLimit(q.Limit)
	tx := j.handle.WithContext(ctx).Model(&readModel{})

	if q.ID != 0 {
		tx = tx.Where("id = ?", q.ID)
	}
	if !q.Since.IsZero() {
		tx = tx.Where("occurred_at >= ?", q.Since.UTC())
	}
	if !q.Until.IsZero() {
		tx = tx.Where("occurred_at < ?", q.Until.UTC())
	}
	if len(q.Actions) > 0 {
		tx = tx.Where("action IN ?", q.Actions)
	}
	if len(q.Outcomes) > 0 {
		tx = tx.Where("outcome IN ?", q.Outcomes)
	}
	if len(q.Actors) > 0 {
		tx = tx.Where("actor IN ?", q.Actors)
	}
	if q.After != "" {
		after, err := strconv.ParseUint(q.After, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("%w: the cursor %q is not one this read face handed out", ErrInvalidQuery, q.After)
		}
		tx = tx.Where("id < ?", after)
	}

	// One row past the page tells whether there is a next one without a
	// second read.
	var models []readModel
	if err := tx.Order("id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrReadFailed, err)
	}

	next := ""
	if len(models) > limit {
		models = models[:limit]
		next = strconv.FormatUint(models[limit-1].ID, 10)
	}

	rows := make([]Row, 0, len(models))
	for _, m := range models {
		tenantID := ""
		if m.TenantID != nil {
			tenantID = *m.TenantID
		}
		detail, err := decodeDetail(m.Detail)
		if err != nil {
			return nil, "", err
		}
		rows = append(rows, Row{
			ID:         m.ID,
			OccurredAt: m.OccurredAt.UTC(),
			Actor:      m.Actor,
			Tenant:     tenantID,
			Source:     m.Source,
			Action:     m.Action,
			Object:     m.Object,
			Outcome:    m.Outcome,
			Purpose:    m.Purpose,
			Detail:     detail,
		})
	}
	return rows, next, nil
}

func pageLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultPageSize
	case limit > MaxPageSize:
		return MaxPageSize
	default:
		return limit
	}
}

func decodeDetail(text string) (map[string]string, error) {
	if text == "" || text == "{}" {
		return map[string]string{}, nil
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		return nil, fmt.Errorf("%w: the detail column does not hold a set of strings: %w", ErrReadFailed, err)
	}
	if detail == nil {
		detail = map[string]string{}
	}
	return detail, nil
}
