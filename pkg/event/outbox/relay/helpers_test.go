package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

type engine struct {
	name   string
	locate func(t *testing.T) string
	open   func(t *testing.T, dsn string) db.Database
}

var engines = []engine{
	{
		name:   "sqlite",
		locate: func(t *testing.T) string { return filepath.Join(t.TempDir(), "delivered.db") },
		open:   func(t *testing.T, dsn string) db.Database { return dbtest.SQLite(t, dsn, nil) },
	},
}

func onEachEngine(t *testing.T, run func(t *testing.T, e engine, dsn string)) {
	t.Helper()
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) { run(t, e, e.locate(t)) })
	}
}

func migrated(t *testing.T, e engine, dsn string) db.Database {
	t.Helper()
	database := e.open(t, dsn)
	var present int64
	if err := database.DB().Raw(tablePresent(database.Dialect())).Row().Scan(&present); err != nil {
		t.Fatalf("looking for the staged table: %v", err)
	}
	if present > 0 {
		return database
	}
	files, err := fs.Glob(migrationsFS, string(database.Dialect())+"/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("the declared migrations hold no %s files: %v", database.Dialect(), err)
	}
	sort.Strings(files)
	for _, name := range files {
		body, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if err := database.DB().Exec(string(body)).Error; err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
	}
	if err := database.DB().Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	return database
}

func tablePresent(dialect db.Dialect) string {
	if dialect == db.Postgres {
		return `SELECT count(*) FROM information_schema.tables WHERE table_name = '` + tableName + `'`
	}
	return `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = '` + tableName + `'`
}

const createOrders = `CREATE TABLE orders (id TEXT PRIMARY KEY, note TEXT NOT NULL)`

var quick = timing{
	poll:      25 * time.Millisecond,
	burst:     2 * time.Millisecond,
	claim:     4 * time.Second,
	publish:   2 * time.Second,
	statement: 10 * time.Second,
	retry:     20 * time.Millisecond,
	retryCap:  100 * time.Millisecond,
	grace:     2 * time.Second,
	cutoff:    time.Second,
	batch:     8,
}

const settle = 10 * time.Second

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type records struct {
	mu   sync.Mutex
	kept []slog.Record
}

func (r *records) Enabled(context.Context, slog.Level) bool { return true }

func (r *records) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	r.kept = append(r.kept, rec.Clone())
	r.mu.Unlock()
	return nil
}

func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }

func (r *records) atLeast(level slog.Level) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, rec := range r.kept {
		if rec.Level >= level {
			out = append(out, rec.Message)
		}
	}
	return out
}

func newQuick(t *testing.T, database db.Database, bus event.Bus, tm timing) *relay {
	t.Helper()
	return newLogged(t, database, bus, tm, discard)
}

func newLogged(t *testing.T, database db.Database, bus event.Bus, tm timing, logger *slog.Logger) *relay {
	t.Helper()
	r, err := newRelay(database, bus, logger, tm)
	if err != nil {
		t.Fatalf("building the relay: %v", err)
	}
	t.Cleanup(func() {
		if err := r.stop(context.Background()); err != nil {
			t.Errorf("stopping the relay: %v", err)
		}
	})
	return r
}

type ledger struct {
	mu       sync.Mutex
	accepted []delivery
}

type delivery struct {
	bus string
	evt event.Event
}

func (l *ledger) events() []event.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]event.Event, 0, len(l.accepted))
	for _, d := range l.accepted {
		out = append(out, d.evt)
	}
	return out
}

func (l *ledger) deliveries() []delivery {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]delivery(nil), l.accepted...)
}

func (l *ledger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.accepted)
}

type recordingBus struct {
	name   string
	ledger *ledger
	delay  time.Duration
	// busy counts the publishes that have not returned yet, so a case can wait
	// for the bus to have no delivery in hand before counting what it accepted.
	busy atomic.Int32

	mu     sync.Mutex
	refuse func(event.Event) error
	// hold, when set, is waited on by every publish before it is accepted,
	// or until its context ends; held is told each time one starts waiting.
	hold chan struct{}
	held chan event.Event
}

func newRecordingBus(name string, l *ledger) *recordingBus {
	return &recordingBus{name: name, ledger: l}
}

func (b *recordingBus) Subscribe(string, string, event.Handler) {}

func (b *recordingBus) Publish(ctx context.Context, evt event.Event) error {
	b.busy.Add(1)
	defer b.busy.Add(-1)
	if b.delay > 0 {
		time.Sleep(b.delay)
	}
	b.mu.Lock()
	refuse, hold, held := b.refuse, b.hold, b.held
	b.mu.Unlock()
	if refuse != nil {
		if err := refuse(evt); err != nil {
			return err
		}
	}
	if hold != nil {
		if held != nil {
			held <- evt
		}
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.ledger.mu.Lock()
	b.ledger.accepted = append(b.ledger.accepted, delivery{bus: b.name, evt: evt})
	b.ledger.mu.Unlock()
	return nil
}

func (b *recordingBus) setRefuse(refuse func(event.Event) error) {
	b.mu.Lock()
	b.refuse = refuse
	b.mu.Unlock()
}

func (b *recordingBus) inflight() int32 { return b.busy.Load() }

var errRefused = errors.New("the bus refuses this event for now")

func eventually(cond func() bool) bool {
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func stagedRows(t *testing.T, database db.Database) int64 {
	t.Helper()
	var n int64
	if err := database.DB().Raw(`SELECT count(*) FROM ` + tableName).Row().Scan(&n); err != nil {
		t.Fatalf("counting the staged rows: %v", err)
	}
	return n
}

func claimedRows(t *testing.T, database db.Database) int64 {
	t.Helper()
	var n int64
	if err := database.DB().Raw(`SELECT count(*) FROM ` + tableName + ` WHERE claimed_by IS NOT NULL`).
		Row().Scan(&n); err != nil {
		t.Fatalf("counting the claimed rows: %v", err)
	}
	return n
}

func waitSettled(t *testing.T, database db.Database, buses ...*recordingBus) {
	t.Helper()
	quiet := func() bool {
		if stagedRows(t, database) != 0 || claimedRows(t, database) != 0 {
			return false
		}
		for _, b := range buses {
			if b.inflight() != 0 {
				return false
			}
		}
		return true
	}
	if !eventually(quiet) {
		var delivery int32
		for _, b := range buses {
			delivery += b.inflight()
		}
		t.Fatalf("the table still holds %d rows (%d claimed) and %d deliveries are in hand after the "+
			"relays should have settled", stagedRows(t, database), claimedRows(t, database), delivery)
	}
}

// stopRelays ends the relays and waits for their loops to return, so that no
// publish can follow the count taken afterwards. stop is idempotent, so the
// cleanup registered by newQuick runs it again harmlessly.
func stopRelays(t *testing.T, relays ...*relay) {
	t.Helper()
	for _, r := range relays {
		if err := r.stop(context.Background()); err != nil {
			t.Fatalf("stopping the relay: %v", err)
		}
	}
}

type refusal struct {
	attempts  int
	lastError string
	claimedBy string
	dueIn     time.Duration
}

func refusalOf(t *testing.T, database db.Database, eventID string) refusal {
	t.Helper()
	stmts, err := statementsFor(database.Dialect())
	if err != nil {
		t.Fatalf("the statements of %s: %v", database.Dialect(), err)
	}
	var (
		row       refusal
		lastError sql.NullString
		claimedBy sql.NullString
		dueIn     int64
	)
	query := `SELECT attempts, last_error, claimed_by, available_at - ` + stmts.now + ` FROM ` + tableName +
		` WHERE event_id = ?`
	if err := database.DB().Raw(query, eventID).Row().Scan(&row.attempts, &lastError, &claimedBy, &dueIn); err != nil {
		t.Fatalf("reading the row staged under %s: %v", eventID, err)
	}
	row.lastError, row.claimedBy = lastError.String, claimedBy.String
	row.dueIn = time.Duration(dueIn) * time.Millisecond
	return row
}

type order struct {
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

type placed struct {
	OrderID string `json:"order_id"`
	// Amount is past what a float64 holds, so a payload that took a trip
	// through a float arrives rounded.
	Amount int64 `json:"amount"`
}

const pastFloat int64 = 1<<53 + 1

func placeOrder(ctx context.Context, database db.Database, o outboxStager, id, eventID string, abandon bool) error {
	errAbandoned := errors.New("the caller abandoned the order")
	err := database.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&order{ID: id, Note: "placed"}).Error; err != nil {
			return err
		}
		if err := o.Stage(tx, event.Event{
			ID: eventID, Type: "order.placed", TenantID: "acme",
			Payload: placed{OrderID: id, Amount: pastFloat},
		}); err != nil {
			return err
		}
		if abandon {
			return errAbandoned
		}
		return nil
	})
	if abandon && errors.Is(err, errAbandoned) {
		return nil
	}
	return err
}

type outboxStager interface {
	Stage(tx *gorm.DB, evt event.Event) error
}

func idsOf(events []event.Event) []string {
	ids := make([]string, 0, len(events))
	for _, evt := range events {
		ids = append(ids, evt.ID)
	}
	return ids
}

func orderID(i int) string { return fmt.Sprintf("o%03d", i) }
