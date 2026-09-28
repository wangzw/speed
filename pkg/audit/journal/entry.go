package journal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/tenant"
)

// tableName is where audit rows land.
//
// It is a constant rather than something read off the model, because the
// statement-level plugin compares it against the table a statement targets in
// order to leave this module's own writes alone. A model whose TableName
// answered something else would part the two, and the parting is a recursion
// rather than a quiet divergence.
const tableName = "audit_entries"

// row is one audit row.
//
// The tenant column is nullable and the model embeds no convention, so the
// tenant isolation plugin leaves it alone. That is deliberate: a cross-tenant
// operation and a system-level operation have no current tenant, and a column
// the plugin filled and constrained would make their entries unwritable. What
// the plugin reads to decide is whether a model embeds the convention, and this
// one does not.
type row struct {
	ID         uint64    `gorm:"primaryKey"`
	OccurredAt time.Time `gorm:"not null;index"`
	Actor      string    `gorm:"not null"`
	TenantID   *string
	Source     string `gorm:"not null"`
	Action     string `gorm:"not null"`
	Object     string `gorm:"not null"`
	Outcome    string `gorm:"not null"`
	Purpose    string `gorm:"not null"`
	Detail     string `gorm:"not null"`
}

// TableName is the name the statement-level capture compares against, and the
// name the migration files create.
func (row) TableName() string { return tableName }

// Record takes over one entry. It is the record seam.
//
// It is behind the same gate the request and event paths are: the seam belongs
// to the three paths that begin at Start. A caller that asks before then, or
// after Stop, is told rather than silently dropped — those three drop silently
// because there is nobody to tell.
//
// The error goes back to the caller and to the failure signal both. A caller
// that wants its own operation to stand or fall with the record can act on it;
// the capture paths inside this module never do.
func (j *journal) Record(ctx context.Context, e audit.Entry) error {
	if !j.accepting.Load() {
		return audit.ErrNotAccepting
	}
	err := j.write(ctx, audit.SourceSeam, e, "")
	j.note(err)
	return err
}

// write puts one entry in the table.
//
// The context travels with the write. Its values are where the actor and the
// tenant are read from; its cancellation bounds the write, which on the seam is
// the caller's to set — a caller that gives up on its own operation is told the
// entry was not written. The paths this module drives detach from the
// cancellation before they get here; see submit.
//
// tenantOverride carries the tenant an event was published under. The bus
// carries it on the event precisely so that a subscriber does not have to
// reconstruct it, and a subscription callback usually has no request context to
// read one from; the context wins when it has one, because that is the tenant
// the work was done under.
func (j *journal) write(ctx context.Context, src audit.Source, e audit.Entry, tenantOverride string) error {
	return insert(j.handle.WithContext(ctx), assemble(ctx, src, e, tenantOverride))
}

// submit is how the capture paths this module drives record an entry: the
// failure is reported and the caller carries on, because a request or an event
// must not fail on account of the record of it.
//
// The write keeps the context's values and drops its cancellation. By the time
// either path writes, the fact it records has happened — the handler has
// returned, the event was published — while the context is one that ends with
// its client or its publisher: net/http cancels a request's context when the
// client closes the connection, and a process-local bus hands a subscriber the
// publisher's own. Writing under that cancellation loses the row for a reason
// that has nothing to do with the table, and reports a healthy table as failing.
// The price is that nothing bounds the write but the database's own timeouts,
// so a slow or locked table slows every request and event it records.
func (j *journal) submit(ctx context.Context, src audit.Source, e audit.Entry, tenantOverride string) {
	if !j.accepting.Load() {
		return
	}
	j.note(j.write(context.WithoutCancel(ctx), src, e, tenantOverride))
}

// note routes one write's outcome to the failure signal.
func (j *journal) note(err error) {
	if err == nil {
		j.signal.recover()
		return
	}
	j.signal.fail(err)
}

// assemble fills in the half of a row the caller does not supply.
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
		Detail:     encodeDetail(maskDetail(e.Detail)),
	}
}

// insert writes one row through the given handle.
func insert(handle *gorm.DB, r row) error {
	if err := handle.Create(&r).Error; err != nil {
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}
	return nil
}

// actorOf reads the authenticated identity out of the context, as a string.
//
// The identifier's own type stays out of this package's contract; what lands in
// the column is its string form. An absent identity is not a refusal: a system
// operation and an exempt route both run without one, and both are things worth
// recording.
func actorOf(ctx context.Context) string {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return ""
	}
	return id.Key()
}

// tenantOf reads the tenant out of the context, falling back to the one an
// event was published under. An absent tenant is not a refusal either.
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
