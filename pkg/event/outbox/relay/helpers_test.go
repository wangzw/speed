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

// engine opens the database capability on one engine, the way an assembly
// delivers it. Each call on one locator is another assembly's pool on the same
// database, which is what two replicas hold.
type engine struct {
	name string
	// locate hands back a locator for a database of the case's own.
	locate func(t *testing.T) string
	// open delivers the capability over a locator.
	open func(t *testing.T, dsn string) db.Database
}

// engines are the engines the cases run on in this build. SQLite needs nothing
// installed, so its leg is part of every run; the file behind the integration
// tag appends PostgreSQL, whose leg reads and writes on the compose stack's
// server.
var engines = []engine{
	{
		name:   "sqlite",
		locate: func(t *testing.T) string { return filepath.Join(t.TempDir(), "delivered.db") },
		open:   func(t *testing.T, dsn string) db.Database { return dbtest.SQLite(t, dsn, nil) },
	},
}

// onEachEngine runs a case on every engine, each with a database of its own.
func onEachEngine(t *testing.T, run func(t *testing.T, e engine, dsn string)) {
	t.Helper()
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) { run(t, e, e.locate(t)) })
	}
}

// migrated delivers the capability over dsn and builds the table this module
// declares, from the files it declares, together with the orders table the
// cases write. Only the first delivery on a locator builds them.
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

// tablePresent counts the staged tables a dialect's catalogue holds.
func tablePresent(dialect db.Dialect) string {
	if dialect == db.Postgres {
		return `SELECT count(*) FROM information_schema.tables WHERE table_name = '` + tableName + `'`
	}
	return `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = '` + tableName + `'`
}

// createOrders is the table standing for a host's write.
const createOrders = `CREATE TABLE orders (id TEXT PRIMARY KEY, note TEXT NOT NULL)`

// quick is the timing the cases run by: the same shape as the module's, on a
// scale a case can wait out.
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

// settle bounds how long a case waits for the relay to reach a state.
const settle = 10 * time.Second

// discard is a logger a case does not read.
var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// records keeps what a relay wrote to its logger.
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

// atLeast lists the messages of the records at level or above.
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

// newQuick builds a relay on database by the quick timing, stopped when the
// case ends.
func newQuick(t *testing.T, database db.Database, bus event.Bus, tm timing) *relay {
	t.Helper()
	return newLogged(t, database, bus, tm, discard)
}

// newLogged is newQuick writing to logger.
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

// ledger records what a bus accepted, across every bus that shares it.
type ledger struct {
	mu       sync.Mutex
	accepted []delivery
}

// delivery is one event a bus accepted, and which bus accepted it.
type delivery struct {
	bus string
	evt event.Event
}

// events is every event the ledger holds, in the order accepted.
func (l *ledger) events() []event.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]event.Event, 0, len(l.accepted))
	for _, d := range l.accepted {
		out = append(out, d.evt)
	}
	return out
}

// deliveries is every delivery the ledger holds.
func (l *ledger) deliveries() []delivery {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]delivery(nil), l.accepted...)
}

// count is how many events the ledger holds.
func (l *ledger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.accepted)
}

// recordingBus is a bus a case steers: it can refuse an event, hold a publish
// until the case lets it go, and take a while over each one. What it accepts
// goes into its ledger.
type recordingBus struct {
	name   string
	ledger *ledger
	// delay is how long each publish takes.
	delay time.Duration
	// busy counts the publishes that have not returned yet, so a case can wait
	// for the bus to have no delivery in hand before counting what it accepted.
	busy atomic.Int32

	mu sync.Mutex
	// refuse decides whether an event is refused; nil accepts every one.
	refuse func(event.Event) error
	// hold, when set, is waited on by every publish before it is accepted,
	// or until its context ends; held is told each time one starts waiting.
	hold chan struct{}
	held chan event.Event
}

// newRecordingBus builds a bus writing into l.
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

// setRefuse replaces the refusal rule.
func (b *recordingBus) setRefuse(refuse func(event.Event) error) {
	b.mu.Lock()
	b.refuse = refuse
	b.mu.Unlock()
}

// inflight is how many publishes the bus has accepted but not yet returned.
func (b *recordingBus) inflight() int32 { return b.busy.Load() }

// errRefused is what a steered bus refuses with.
var errRefused = errors.New("the bus refuses this event for now")

// eventually polls cond until it holds or the settle bound runs out.
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

// stagedRows counts the rows the table holds.
func stagedRows(t *testing.T, database db.Database) int64 {
	t.Helper()
	var n int64
	if err := database.DB().Raw(`SELECT count(*) FROM ` + tableName).Row().Scan(&n); err != nil {
		t.Fatalf("counting the staged rows: %v", err)
	}
	return n
}

// claimedRows counts the staged rows a relay currently holds: rows it has taken
// and has not yet published or given back.
func claimedRows(t *testing.T, database db.Database) int64 {
	t.Helper()
	var n int64
	if err := database.DB().Raw(`SELECT count(*) FROM ` + tableName + ` WHERE claimed_by IS NOT NULL`).
		Row().Scan(&n); err != nil {
		t.Fatalf("counting the claimed rows: %v", err)
	}
	return n
}

// waitSettled waits, within the settle bound, for the table to hold nothing a
// relay is still working on and for the given buses to have no publish in
// hand: every staged row has been published and removed, no relay holds a
// claim, and nothing accepted is still being delivered. It is the source-side
// condition a count of what the bus accepted is judged on — once the table has
// nothing left to relay and no delivery is under way, nothing more can be
// published from it.
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

// refusal is what one staged row records of the refusals it met, and of its
// claim.
type refusal struct {
	attempts  int
	lastError string
	claimedBy string
	// dueIn is how long until the row falls due, by the database's clock;
	// zero or less when it is due now.
	dueIn time.Duration
}

// refusalOf reads the row staged under eventID.
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

// order is one row of the orders table.
type order struct {
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

// placed is the payload the cases stage.
type placed struct {
	OrderID string `json:"order_id"`
	// Amount is past what a float64 holds, so a payload that took a trip
	// through a float arrives rounded.
	Amount int64 `json:"amount"`
}

// pastFloat is 2^53 + 1, which a float64 rounds to 2^53.
const pastFloat int64 = 1<<53 + 1

// placeOrder writes an order and stages its announcement in one transaction,
// which then commits or, when abandon is set, rolls back.
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

// outboxStager is what placeOrder stages through.
type outboxStager interface {
	Stage(tx *gorm.DB, evt event.Event) error
}

// idsOf lists the identifiers of events.
func idsOf(events []event.Event) []string {
	ids := make([]string, 0, len(events))
	for _, evt := range events {
		ids = append(ids, evt.ID)
	}
	return ids
}

// orderID names the i-th order of a case.
func orderID(i int) string { return fmt.Sprintf("o%03d", i) }
