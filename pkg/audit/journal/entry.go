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
//
// The event column holds the identifier of the event an event-path row
// records, and is null on every other row. It is unique, which is what makes a
// second delivery of one event — to another replica, or to this one again —
// leave the row the first delivery wrote; see insert.
//
// A host reading the table for one tenant's trail relies on three facts, and
// they are part of this module's contract rather than of its schema: the tenant
// column is tenant_id and holds the identifier's text, or NULL where there is
// none, never an empty string; the id key is unique over the whole table; and
// this module is the table's only writer.
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
	EventID    *string
}

// eventColumn is the column the event identifier is kept in, and the conflict
// target the event path's insert names.
const eventColumn = "event_id"

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
	err := j.write(ctx, assemble(ctx, audit.SourceSeam, e, ""))
	j.note(err)
	return err
}

// write puts one row in the table.
//
// The context travels with the write. Its values are where the actor and the
// tenant were read from; its cancellation bounds the write, which on the seam is
// the caller's to set — a caller that gives up on its own operation is told the
// entry was not written. The paths this module drives detach from the
// cancellation before they get here; see submit.
//
// The row goes out on a connection of its own, taken from the delivered pool
// through db.Connection within connectionWait. On a database held to a single
// connection — an in-memory one — a caller that records or publishes inside its
// own transaction on the delivered handle holds that connection until after
// this write returns, and waiting for it is waiting for the caller itself:
// under a context with no deadline, and on the paths that drop the cancellation
// under any context, nothing else would end that wait. Past the bound the write
// fails with ErrWriteFailed. The insert is then one statement, in the
// transaction GORM opens around a write, as it would be on the handle itself.
//
// The insert waits for a lock another connection holds as the engine waits for
// it, which the bound does not reach. On SQLite that is the connection's busy
// timeout, the driver's five seconds unless the locator sets another: a caller
// that wrote to the same database file in its own transaction holds the write
// lock the insert waits for, and the insert fails once the busy timeout has
// passed, whatever the context. On PostgreSQL it is until the context ends, or
// the session's lock_timeout or statement_timeout does; an insert into the
// audit table meets a lock there when something holds the table itself, or
// when another insert of the same event's row has not yet committed.
// A lock the engine gave up on fails the write with ErrWriteFailed and the
// engine's refusal.
func (j *journal) write(ctx context.Context, r row) error {
	err := db.Connection(ctx, j.handle, connectionWait, func(session *gorm.DB) error {
		return insert(session, r)
	})
	if err != nil && !errors.Is(err, audit.ErrWriteFailed) {
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}
	return err
}

// connectionWait is how long the rows this module writes itself — the seam's,
// the request path's and the event path's — wait for a connection of the
// delivered pool, through db.Connection. A connection taken under load comes
// free in the time a statement takes, far inside it.
const connectionWait = 5 * time.Second

// submit is how the capture paths this module drives record a row: the failure
// is reported and the caller carries on, because a request or an event must not
// fail on account of the record of it.
//
// The write keeps the context's values and drops its cancellation. By the time
// either path writes, the fact it records has happened — the handler has
// returned, the event was published — while the context is one that ends with
// its client or its publisher: net/http cancels a request's context when the
// client closes the connection, and a process-local bus hands a subscriber the
// publisher's own. Writing under that cancellation loses the row for a reason
// that has nothing to do with the table, and reports a healthy table as failing.
// The price is that connectionWait bounds the wait for a connection alone, and
// the database's own timeouts are the only bound on the insert's wait for a
// lock and on the insert itself: a slow or locked table slows every request and
// event it records by as long as those let it wait — on SQLite the busy
// timeout, and on PostgreSQL with neither a lock_timeout nor a
// statement_timeout, for as long as the lock is held.
func (j *journal) submit(ctx context.Context, r row) {
	if !j.accepting.Load() {
		return
	}
	j.note(j.write(context.WithoutCancel(ctx), r))
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
//
// tenantOverride carries the tenant an event was published under. The bus
// carries it on the event precisely so that a subscriber does not have to
// reconstruct it, and a subscription callback usually has no request context to
// read one from; the context wins when it has one, because that is the tenant
// the work was done under.
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

// withImpersonation returns the detail as it goes into the row. The
// impersonation keys are the journal's alone: an impersonating identity has them
// written from it, and on every other row, including one with no identity, the
// caller's keys under those names are removed. The caller's map is not changed.
//
// The actor column already says who did it, because it carries the identity's
// key, which is the actor's account; the detail says on whose behalf. The row
// records what the identity in the context says, and not what a caller of this
// module claims it said.
func withImpersonation(ctx context.Context, detail map[string]string) map[string]string {
	id, ok := authn.FromContext(ctx)
	if !ok || !id.Impersonating() {
		return withoutImpersonationKeys(detail)
	}
	scope, _ := id.Scope()
	return impersonationKeys(detail, string(id.Account()), scope, string(id.Session()))
}

// withoutImpersonationKeys returns the detail without the impersonation keys, as
// it was handed over when it holds none of them.
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

// impersonationKeys returns a copy of the detail with the impersonation keys
// written from the given values over whatever the caller put under them. An empty
// session writes no session key and removes the caller's, so that a row never
// carries a session the identity does not have.
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

// insert writes one row through the given handle.
//
// A row that records an event is written only if no row records that event
// yet. A bus promises no delivery count: a transport hands every event to every
// replica's subscription, and a backend may hand it to one of them again, while
// every replica writes into the one table. The event's identifier is the same
// on every delivery, so a later insert meets the unique key the first one left
// and does nothing — whichever delivery gets there first writes the row, and the
// rest succeed without writing. That is not a swallowed failure: the fact is in
// the table, which is what each of them was writing for.
func insert(handle *gorm.DB, r row) error {
	if err := create(handle, r); err != nil {
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}
	return nil
}

// create is insert without the sentinel, for a caller that names the cause of a
// failure before wrapping it.
func create(handle *gorm.DB, r row) error {
	if r.EventID != nil {
		handle = handle.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: eventColumn}},
			DoNothing: true,
		})
	}
	return handle.Create(&r).Error
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
