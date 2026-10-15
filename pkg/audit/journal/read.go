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

// ErrInvalidQuery reports a query this read face cannot read: a cursor that is
// not one it handed out. It is a shape failure and not a table failure — the
// read face never reaches the table with it — so a caller answers it as a bad
// request, while every other failure this read face reports is an outage.
var ErrInvalidQuery = errors.New("audit: the query is not one this read face can read")

// ErrReadFailed reports that the audit table could not be read. It is about the
// module's own state — its connection, its table, a column that does not hold
// what the writer puts there — and not about the query, which was well formed
// enough to be issued.
var ErrReadFailed = errors.New("audit: the audit table could not be read")

// DefaultPageSize is the page size a query that asks for none gets.
const DefaultPageSize = 50

// MaxPageSize is the largest page a query can ask for. A query that asks for
// more is read at this size.
const MaxPageSize = 200

// Reader is the read surface of the audit table, delivered by this module beside
// the capture paths. It is one module with the capture paths rather than a
// module of its own, because the table and the record shape are one: this reads
// exactly what the capture paths write.
//
// The module delivers it exclusively, on the same judgement as the audit
// capability it delivers beside — two readers in one assembly are two tables an
// entry could be read from, and which one a read reached is not a fact anything
// could resolve.
//
// It reads, and only reads. Nothing here writes the table: the table is written
// by the capture paths alone, and a write through this surface would be a row no
// capture path produced, indistinguishable from a row the module wrote.
type Reader interface {
	// Query reads one page of audit rows, newest first, returning the page and
	// the cursor of the next, which is empty on the last page.
	//
	// The tenant is a fact of the context and not of the query. A context that
	// carries a tenant scopes every row read to it; the cross-tenant mark lifts
	// the condition and reads every tenant's rows and the rows that belong to no
	// tenant; a context that is neither is refused by the tenancy capability, as
	// every scoped read of this table is.
	//
	// A query whose cursor is not one this surface handed out is ErrInvalidQuery.
	// A table that could not be read is ErrReadFailed.
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

// readModel is the read-only model of the audit table.
//
// It embeds the tenancy convention, so that the tenancy capability scopes every
// read through it — to a request's tenant under a context that carries one, to
// nothing under the cross-tenant mark, and refuses a read whose context is
// neither. That is the whole of how a read of this table is held to a tenant;
// this module states no tenant condition of its own.
//
// It embeds the convention to READ a table another module declares and writes,
// not to declare one: it carries none of the obligations that bind the writing
// side — the key led by the tenant column, the NOT NULL — because it issues no
// write. It issues read statements alone.
//
// TenantID takes the tenant column over from the convention's own field. The
// convention reads a NULL into the empty identifier and so cannot tell a row
// that belongs to no tenant from one whose tenant column holds the empty
// string; this column is nullable and holds a pointer, so a NULL reads as nil
// and the two are told apart. A tenant's read still scopes on this field.
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

	TenantID *string `gorm:"column:tenant_id"`
}

// TableName is the name the migration files create and the table the capture
// paths write.
func (readModel) TableName() string { return tableName }

// Query reads one page of the audit table. See Reader for the contract.
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
	if q.After != "" {
		after, err := strconv.ParseUint(q.After, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("%w: the cursor %q is not one this read face handed out", ErrInvalidQuery, q.After)
		}
		tx = tx.Where("id < ?", after)
	}

	// One row past the page tells whether there is a next one without a second
	// read. The rows come newest first: the row identifier is a serial, so a
	// greater identifier is a later row, and reading by the key rather than by
	// an offset costs the same however deep a page is.
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
		// The tenant column is the one the convention scopes on, which this
		// model's own pointer field took over; a NULL is nil, which is "no
		// tenant".
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

// pageLimit resolves a query's page size: a non-positive value takes the
// default, and a value above the maximum is read at the maximum.
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

// decodeDetail reads the detail column, the inverse of encodeDetail. An empty
// column and an empty object are the empty set. A column that does not hold a
// set of strings is ErrReadFailed: the writer puts nothing else there, so its
// presence means the table holds a row this module did not write.
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
