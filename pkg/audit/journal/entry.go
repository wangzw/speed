package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/tenant"
)

const tableName = "audit_entries"

type row struct {
	ID         uint64    `gorm:"primaryKey"`
	OccurredAt time.Time `gorm:"not null;index"`
	Actor      string    `gorm:"not null"`
	// TenantID stays nullable and the row embeds no tenant convention: a
	// cross-tenant or system-level entry has no current tenant, and a filled or
	// constrained column would make it unwritable.
	TenantID *string
	Source   string `gorm:"not null"`
	Action   string `gorm:"not null"`
	Object   string `gorm:"not null"`
	Outcome  string `gorm:"not null"`
	Purpose  string `gorm:"not null"`
	Detail   string `gorm:"not null"`
	EventID  *string
}

const eventColumn = "event_id"

func (row) TableName() string { return tableName }

func (j *journal) Record(ctx context.Context, e audit.Entry) error {
	if !j.accepting.Load() {
		return audit.ErrNotAccepting
	}
	err := j.write(ctx, assemble(ctx, audit.SourceSeam, e, ""))
	j.note(err)
	return err
}

func (j *journal) write(ctx context.Context, r row) error {
	err := db.Connection(ctx, j.handle, j.connectWithin, func(session *gorm.DB) error {
		return insert(session, r)
	})
	if err != nil && !errors.Is(err, audit.ErrWriteFailed) {
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}
	return err
}

const connectionWait = 5 * time.Second

// submit is how the capture paths this module drives record a row: the
// failure is reported and the caller carries on, because a request or an
// event must not fail on account of the record of it.
func (j *journal) submit(ctx context.Context, r row) {
	if !j.accepting.Load() {
		return
	}
	j.note(j.write(context.WithoutCancel(ctx), r))
}

func (j *journal) note(err error) {
	if err == nil {
		j.signal.recover()
		return
	}
	j.signal.fail(err)
}

func assemble(ctx context.Context, src audit.Source, e audit.Entry, tenantOverride string) row {
	return row{
		OccurredAt: time.Now().UTC(),
		Actor:      actorOf(ctx),
		TenantID:   tenantOf(ctx, tenantOverride),
		Source:     string(src),
		Action:     e.Action,
		Object:     e.Object,
		Outcome:    e.Outcome,
		Purpose:    e.Purpose,
		Detail:     encodeDetail(maskDetail(withImpersonation(ctx, e.Detail))),
	}
}

// The detail keys this module writes from an impersonating identity. They are a
// closed set of this module's own: the caller's detail does not get to name them.
const (
	detailOnBehalfOf         = "on_behalf_of"
	detailImpersonationScope = "impersonation_scope"
	detailSession            = "session"
)

func withImpersonation(ctx context.Context, detail map[string]string) map[string]string {
	id, ok := authn.FromContext(ctx)
	if !ok || !id.Impersonating() {
		return withoutImpersonationKeys(detail)
	}
	scope, _ := id.Scope()
	return impersonationKeys(detail, string(id.Account()), scope, string(id.Session()))
}

func withoutImpersonationKeys(detail map[string]string) map[string]string {
	_, onBehalfOf := detail[detailOnBehalfOf]
	_, scope := detail[detailImpersonationScope]
	_, session := detail[detailSession]
	if !onBehalfOf && !scope && !session {
		return detail
	}
	out := maps.Clone(detail)
	delete(out, detailOnBehalfOf)
	delete(out, detailImpersonationScope)
	delete(out, detailSession)
	return out
}

func impersonationKeys(detail map[string]string, account, scope, session string) map[string]string {
	out := make(map[string]string, len(detail)+3)
	maps.Copy(out, detail)
	out[detailOnBehalfOf] = account
	out[detailImpersonationScope] = scope
	if session != "" {
		out[detailSession] = session
	} else {
		delete(out, detailSession)
	}
	return out
}

func insert(handle *gorm.DB, r row) error {
	if err := create(handle, r); err != nil {
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}
	return nil
}

func create(handle *gorm.DB, r row) error {
	if r.EventID != nil {
		handle = handle.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: eventColumn}},
			DoNothing: true,
		})
	}
	return handle.Create(&r).Error
}

func actorOf(ctx context.Context) string {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return ""
	}
	return id.Key()
}

func tenantOf(ctx context.Context, fallback string) *string {
	if id, ok := tenant.FromContext(ctx); ok {
		value := string(id)
		return &value
	}
	if fallback == "" {
		return nil
	}
	value := fallback
	return &value
}

// encodeDetail renders the detail as the text the column holds. An empty detail
// is written as an empty object rather than a null, so that a reader can tell
// "no detail" from "the column was never written".
func encodeDetail(d map[string]string) string {
	if len(d) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		// A map of strings cannot fail to marshal. Reaching this would mean
		// the type changed under it, and writing an empty object keeps the
		// row rather than losing it to a panic inside a capture path.
		return "{}"
	}
	return string(encoded)
}
