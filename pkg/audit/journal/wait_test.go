package journal

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

// deliveredBusyTimeout is the busy timeout the delivered locator sets in the
// cases that meet the delivered database's lock. It is not the driver's
// default, so a connection that carries anything but what its locator gave it
// is seen; and it is short, so a row that waits for the lock for it gives up
// well inside a case's timer.
const deliveredBusyTimeout = 1500 * time.Millisecond

// selfWaitTimer is how long a case lets a write run before it counts the write
// as waiting on itself: past the delivered connections' busy timeout and past
// openedRowWait, with room for a loaded machine.
var selfWaitTimer = deliveredBusyTimeout + coretest.Scale(3*time.Second)

// connectionTimer is the same for a row this module writes itself, which waits
// for a connection within connectionWait.
var connectionTimer = connectionWait + coretest.Scale(3*time.Second)

// within runs write on a goroutine of its own and reports what it returned, or
// that it had not returned when the limit ran out. None of the writes carries a
// deadline: the timer is what bounds the case, so a write that waits for good is
// seen waiting rather than cut short by a context the case gave it.
func within(limit time.Duration, write func() error) (bool, error) {
	done := make(chan error, 1)
	go func() { done <- write() }()
	select {
	case err := <-done:
		return true, err
	case <-time.After(limit):
		return false, nil
	}
}

// busyTimeoutOf reads the busy timeout a connection of the pool carries.
func busyTimeoutOf(ctx context.Context, t *testing.T, conn *sql.Conn) int64 {
	t.Helper()
	var ms int64
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Errorf("reading a connection's busy timeout: %v", err)
	}
	return ms
}

// requireBusyTimeoutsAsOpened checks every idle connection of the handle's pool
// for the busy timeout the pool's connections carry when they are opened: the
// rows written through db.Connection leave the connection they take as they
// found it.
func requireBusyTimeoutsAsOpened(ctx context.Context, t *testing.T, handle *gorm.DB) {
	t.Helper()
	pool, err := handle.DB()
	if err != nil {
		t.Errorf("reaching the delivered pool: %v", err)
		return
	}
	var taken []*sql.Conn
	defer func() {
		for _, conn := range taken {
			_ = conn.Close()
		}
	}()
	for pool.Stats().Idle > 0 {
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Errorf("taking an idle connection: %v", err)
			return
		}
		taken = append(taken, conn)
		if got := busyTimeoutOf(ctx, t, conn); got != deliveredBusyTimeout.Milliseconds() {
			t.Errorf("a connection of the delivered pool carries a busy timeout of %dms, want the %v its "+
				"locator gave it", got, deliveredBusyTimeout)
		}
	}
}

// withBusyTimeout is a SQLite locator that opens every connection with
// deliveredBusyTimeout.
func withBusyTimeout(dsn string) string {
	return fmt.Sprintf("%s?_pragma=busy_timeout(%d)", dsn, deliveredBusyTimeout.Milliseconds())
}

// quietly is a session that keeps the record of a statement a case fails on
// purpose out of the run's output.
func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Discard})
}

// requireRefusedForWaiting checks that a write on a connection built with Open
// was refused for its record, and that the refusal names the wait for a
// connection.
func requireRefusedForWaiting(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, audit.ErrWriteFailed) {
		t.Errorf("the write gave %v, want ErrWriteFailed", err)
		return
	}
	if !strings.Contains(err.Error(), "waits no longer") {
		t.Errorf("the refusal %q does not say the row was refused for waiting", err)
	}
}

// lockHeadroom is how far past the delivered connection's busy timeout a write
// may return: a fifth of the timeout, which is room for a loaded machine's
// scheduling and still far short of the driver's own busy timeout, the finite
// wrong time the upper bound rejects in the cases that do not widen it.
const lockHeadroom = 300 * time.Millisecond

// driverBusyTimeout is the busy timeout the SQLite driver applies to a
// connection whose locator sets none: the wait a delivered connection that
// ignored its locator's pragma would give the insert, and the wrong time the
// upper bound rejects where the case does not widen the connection wait.
const driverBusyTimeout = 5 * time.Second

// requireRefusedForTheLock checks that a write whose row met the delivered
// database's lock was refused with ErrWriteFailed and SQLite's refusal of the
// lock, once the delivered connection's busy timeout had passed and not long
// after. wrong is the time the wrong implementation this case rejects needs,
// which the two callers state: the driver's own busy timeout where the case
// leaves the locator's in place, and the widened connection wait where it
// widens it.
func requireRefusedForTheLock(t *testing.T, err error, took, wrong time.Duration) {
	t.Helper()
	if !errors.Is(err, audit.ErrWriteFailed) || !strings.Contains(err.Error(), "SQLITE_BUSY") {
		t.Errorf("the write gave %v, want ErrWriteFailed with SQLite's refusal of the lock", err)
	}
	if took < deliveredBusyTimeout {
		t.Errorf("the write failed after %v, before the delivered connection's busy timeout of %v",
			took, deliveredBusyTimeout)
	}
	if limit := coretest.DeclaredAgainst(t, deliveredBusyTimeout, lockHeadroom, wrong); took > limit {
		t.Errorf("the write failed after %v, well past the delivered connection's busy timeout of %v",
			took, deliveredBusyTimeout)
	}
}

// A write on a connection built with Open inside a transaction the caller holds
// on the delivered handle fails rather than waiting on the caller for as long
// as the caller waits on it.
//
// The row goes to the delivered database, and what it needs there is held by
// the caller until after the write returns. On an in-memory database that is
// the one connection, and the row gives up on it within openedRowWait, naming
// the wait. On a file database it is the write lock, which the row's insert
// waits for for the delivered connection's busy timeout before SQLite refuses
// it. Either way the write it would record does not happen, and the delivered
// connections carry the busy timeout they were opened with.
func TestAWriteOnAnOpenedConnectionInsideTheCallersTransactionFails(t *testing.T) {
	for _, shape := range []struct {
		name string
		dsn  string
		lock bool
	}{
		{name: "a file database", dsn: withBusyTimeout(filepath.Join(t.TempDir(), "delivered.db")), lock: true},
		{name: "an in-memory database", dsn: withBusyTimeout(":memory:")},
	} {
		t.Run(shape.name, func(t *testing.T) {
			assembledAt(t, shape.dsn, nil, func(ctx context.Context, reg *core.Registry) error {
				database, err := core.Resolve[db.Database](reg)
				if err != nil {
					t.Errorf("resolving the database capability: %v", err)
					return errCaseFailed
				}
				handle := database.DB()
				if createErr := handle.Exec(createOrders).Error; createErr != nil {
					t.Errorf("creating the orders table: %v", createErr)
					return errCaseFailed
				}
				opened, err := openElsewhere(ctx, t, database)
				if err != nil {
					return err
				}
				defer func() { _ = db.Close(opened) }()

				var took time.Duration
				returned, err := within(selfWaitTimer, func() error {
					return quietly(handle).Transaction(func(tx *gorm.DB) error {
						if heldErr := tx.Create(&order{ID: "d1", Note: "delivered"}).Error; heldErr != nil {
							return heldErr
						}
						started := time.Now()
						defer func() { took = time.Since(started) }()
						return quietly(opened).WithContext(context.Background()).
							Create(&order{ID: "o1", Note: "elsewhere"}).Error
					})
				})
				if !returned {
					t.Errorf("a write on a connection built with Open inside a transaction on the delivered "+
						"handle was still waiting after %v", selfWaitTimer)
					return errCaseFailed
				}
				if shape.lock {
					// The upper bound in requireRefusedForTheLock is the delivered
					// busy timeout plus lockHeadroom through GATE_TIME_SCALE alone,
					// and the timer around this write carries the race factor as
					// well. A wrong implementation that applies the connection wait
					// here answers before the busy timeout, so the unscaled lower
					// bound catches it; the upper bound rejects one that waits past
					// the busy timeout, which is the driver's own.
					requireRefusedForTheLock(t, err, took, driverBusyTimeout)
				} else {
					requireRefusedForWaiting(t, err)
				}
				var committed int64
				if err := opened.Model(&order{}).Count(&committed).Error; err != nil || committed != 0 {
					t.Errorf("the write was refused, but %d order(s) committed on its database (%v)", committed, err)
				}
				requireBusyTimeoutsAsOpened(ctx, t, handle)
				return nil
			})
		})
	}
}

// holdTheDatabase begins a transaction that holds the whole of the SQLite
// database at file, on a pool of its own, and returns what lets it go.
func holdTheDatabase(ctx context.Context, t *testing.T, database db.Database, file string) (func(), error) {
	t.Helper()
	holding, err := database.Open(ctx, file)
	if err != nil {
		t.Errorf("opening a pool to hold the database with: %v", err)
		return nil, errCaseFailed
	}
	pool, err := holding.DB()
	if err != nil {
		t.Errorf("reaching the holding pool: %v", err)
		return nil, errors.Join(errCaseFailed, db.Close(holding))
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Errorf("taking a connection of the holding pool: %v", err)
		return nil, errors.Join(errCaseFailed, db.Close(holding))
	}
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Errorf("holding the database: %v", err)
		return nil, errors.Join(errCaseFailed, conn.Close(), db.Close(holding))
	}
	return func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		_ = conn.Close()
		_ = db.Close(holding)
	}, nil
}

// A write on a connection built with Open while another connection holds the
// whole delivered database fails once the delivered connection's busy timeout
// has passed: telling that the connection does not reach the delivered table
// waits for no lock, and the row's insert waits for the lock as SQLite makes it
// wait.
func TestAWriteOnAnOpenedConnectionWhileTheDeliveredDatabaseIsHeldFailsOnceTheBusyTimeoutPasses(t *testing.T) {
	file := filepath.Join(t.TempDir(), "delivered.db")
	assembledAt(t, withBusyTimeout(file), nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		handle := database.DB()
		opened, err := openElsewhere(ctx, t, database)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close(opened) }()
		release, err := holdTheDatabase(ctx, t, database, file)
		if err != nil {
			return err
		}

		started := time.Now()
		returned, err := within(selfWaitTimer, func() error {
			return quietly(opened).WithContext(context.Background()).Create(&order{ID: "o1", Note: "elsewhere"}).Error
		})
		took := time.Since(started)
		release()
		if !returned {
			t.Errorf("a write on a connection built with Open while the delivered database was held was "+
				"still waiting after %v", selfWaitTimer)
			return errCaseFailed
		}
		// The same bound as the call site above: the delivered busy timeout plus
		// lockHeadroom through GATE_TIME_SCALE alone for the upper bound, and the
		// unscaled busy timeout for the lower one that catches a connection wait.
		requireRefusedForTheLock(t, err, took, driverBusyTimeout)
		requireBusyTimeoutsAsOpened(ctx, t, handle)
		return nil
	})
}

// A write on a connection built with Open onto the delivered database itself is
// recorded on the statement's own session while that connection's own
// transaction holds the whole database: telling that the connection reaches the
// delivered table waits for no lock, so it is not left waiting on the lock its
// own statement holds.
func TestAConnectionOpenedOntoTheDeliveredDatabaseRecordsWhileItsTransactionHoldsTheDatabase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "delivered.db")
	assembledAt(t, withBusyTimeout(file), nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		handle := database.DB()
		if createErr := handle.Exec(createOrders).Error; createErr != nil {
			t.Errorf("creating the orders table: %v", createErr)
			return errCaseFailed
		}
		// Every transaction on this connection begins by taking the whole
		// database.
		again, err := database.Open(ctx, file+"?_txlock=exclusive")
		if err != nil {
			t.Errorf("opening the delivered database a second time: %v", err)
			return errCaseFailed
		}
		defer func() { _ = db.Close(again) }()

		returned, err := within(selfWaitTimer, func() error {
			return quietly(again).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return tx.Create(&order{ID: "o1", Note: "held"}).Error
			})
		})
		if !returned {
			t.Errorf("a write in a transaction holding the delivered database, on a connection opened onto "+
				"it, was still waiting after %v", selfWaitTimer)
			return errCaseFailed
		}
		if err != nil {
			t.Errorf("a write in a transaction holding the delivered database, on a connection opened onto "+
				"it, gave %v, want it written and recorded", err)
			return errCaseFailed
		}
		if rows := statementRows(t, handle); len(rows) != 1 || rows[0].Object != "orders" {
			t.Errorf("the delivered table holds the statement rows %+v, want the one write", rows)
		}
		return nil
	})
}

// briefHold is how long another writer holds the delivered database in the
// case below: far longer than an insert takes, well inside the busy timeout.
const briefHold = 200 * time.Millisecond

// A write on a connection built with Open while another writer briefly holds
// the delivered database waits the hold out and is recorded.
//
// A row that gave up the moment the delivered database was busy would fail
// every such write whenever any other request happened to be writing there.
func TestAWriteOnAnOpenedConnectionWaitsOutAnotherWritersBriefHold(t *testing.T) {
	for _, shape := range []struct{ name, dsn string }{
		{name: "a file database", dsn: withBusyTimeout(filepath.Join(t.TempDir(), "delivered.db"))},
		{name: "an in-memory database", dsn: withBusyTimeout(":memory:")},
	} {
		t.Run(shape.name, func(t *testing.T) {
			assembledAt(t, shape.dsn, nil, func(ctx context.Context, reg *core.Registry) error {
				database, err := core.Resolve[db.Database](reg)
				if err != nil {
					t.Errorf("resolving the database capability: %v", err)
					return errCaseFailed
				}
				handle := database.DB()
				if createErr := handle.Exec(createOrders).Error; createErr != nil {
					t.Errorf("creating the orders table: %v", createErr)
					return errCaseFailed
				}
				opened, err := openElsewhere(ctx, t, database)
				if err != nil {
					return err
				}
				defer func() { _ = db.Close(opened) }()

				held, released := make(chan struct{}), make(chan error, 1)
				go func() {
					released <- handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
						if err := tx.Create(&order{ID: "d1", Note: "held"}).Error; err != nil {
							return err
						}
						close(held)
						time.Sleep(briefHold)
						return nil
					})
				}()
				select {
				case <-held:
				case err := <-released:
					t.Errorf("the other writer's transaction ended before it held the database: %v", err)
					return errCaseFailed
				}
				// The write waits on purpose, and GORM reports a statement that
				// took this long as slow; the wait is what the case measures.
				start := time.Now()
				if err := quietly(opened).WithContext(ctx).Create(&order{ID: "o1", Note: "elsewhere"}).Error; err != nil {
					t.Errorf("a write on a connection built with Open while another writer held the delivered "+
						"database for %v gave %v, want it written once the hold ended", briefHold, err)
				}
				if waited := time.Since(start); waited < briefHold/2 {
					t.Errorf("the write returned after %v, before the other writer let go: the case did not "+
						"make it wait", waited)
				}
				if err := <-released; err != nil {
					t.Errorf("the other writer's transaction: %v", err)
				}
				if got := statementRows(t, handle); len(got) != 2 {
					t.Errorf("the delivered table holds %d statement row(s), want the held write and the one "+
						"on the second connection", len(got))
				}
				requireBusyTimeoutsAsOpened(ctx, t, handle)
				return nil
			})
		})
	}
}

// openSingle builds a handle on an in-memory database held to one connection —
// the one the database module delivers for :memory:, the shape it gives a
// locator private to its connection — with the capture plugin installed and
// both tables created.
func openSingle(t *testing.T) *gorm.DB {
	t.Helper()
	handle := quietly(dbtest.SQLite(t, ":memory:", nil).DB())
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	applyMigrations(t, handle, auditDialect)
	return handle
}

// A row this module writes itself — handed to the seam, or taken from an event
// a process-local bus delivers on the publisher's goroutine — from inside a
// transaction the caller holds on an in-memory database fails within the
// connection bound instead of waiting for the caller's connection for good.
//
// The in-memory database's one connection plays the lock's part, and nothing
// else ends that wait. The event path drops the publisher's cancellation, so
// there not even a deadline would.
func TestARowWrittenInsideTheCallersTransactionOnOneConnectionDoesNotWaitForIt(t *testing.T) {
	t.Run("the seam", func(t *testing.T) {
		handle := openSingle(t)
		j := newTestJournal(handle, settings{})
		returned, err := within(connectionTimer, func() error {
			return handle.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&order{ID: "o1", Note: "held"}).Error; err != nil {
					return err
				}
				return j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"})
			})
		})
		if !returned {
			t.Fatalf("Record inside the caller's transaction was still waiting after %v", connectionTimer)
		}
		if !errors.Is(err, audit.ErrWriteFailed) {
			t.Errorf("Record inside the caller's transaction gave %v, want ErrWriteFailed", err)
		}
	})
	t.Run("the event path", func(t *testing.T) {
		handle := openSingle(t)
		bus := newRecordingBus()
		j := newTestJournal(handle, settings{events: []string{"order.updated"}})
		var reported bytes.Buffer
		j.signal = &signal{out: &reported}
		j.subscribe(bus)
		returned, err := within(connectionTimer, func() error {
			return handle.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&order{ID: "o1", Note: "held"}).Error; err != nil {
					return err
				}
				return bus.Publish(context.Background(), event.Event{
					ID: event.NewID(), Type: "order.updated", Payload: map[string]any{"object": "order/o1"},
				})
			})
		})
		if !returned {
			t.Fatalf("publishing inside the caller's transaction was still waiting after %v", connectionTimer)
		}
		if err != nil {
			t.Errorf("the publisher's transaction gave %v, want the event's row to fail without failing it", err)
		}
		if !strings.Contains(reported.String(), audit.ErrWriteFailed.Error()) {
			t.Errorf("the lost row was not reported through the failure signal: %q", reported.String())
		}
	})
}

// A row this module writes itself while another connection holds the whole
// delivered database waits for the lock for the delivered connection's busy
// timeout, whatever connectionWait is, and then fails: the seam returns
// ErrWriteFailed with SQLite's refusal. A caller holding the database in its
// own transaction on a file database is what the row waits for there.
func TestARowWrittenWhileTheDeliveredDatabaseIsHeldFailsOnceTheBusyTimeoutPasses(t *testing.T) {
	file := filepath.Join(t.TempDir(), "delivered.db")
	handle := quietly(dbtest.SQLite(t, withBusyTimeout(file), nil).DB())
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	applyMigrations(t, handle, auditDialect)
	j := newTestJournal(handle, settings{})
	// A wrong implementation applies the connection wait where the engine's
	// busy timeout bounds the lock, so widening it makes that implementation
	// need an hour while the right one still gives up after the delivered
	// connection's busy timeout. The bound in requireRefusedForTheLock is that
	// timeout plus lockHeadroom through GATE_TIME_SCALE alone; the hour is the
	// wrong time the case states to it.
	j.connectWithin = time.Hour
	holding, err := dbtest.SQLite(t, file, nil).DB().DB()
	if err != nil {
		t.Fatalf("reaching the holding pool: %v", err)
	}
	holder, err := holding.Conn(t.Context())
	if err != nil {
		t.Fatalf("taking a connection of the holding pool: %v", err)
	}
	if _, err = holder.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("holding the database: %v", err)
	}
	defer func() {
		_, _ = holder.ExecContext(context.Background(), "ROLLBACK")
		_ = holder.Close()
	}()

	started := time.Now()
	returned, err := within(connectionTimer, func() error {
		return j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"})
	})
	if !returned {
		t.Fatalf("Record while the delivered database was held was still waiting after %v", connectionTimer)
	}
	requireRefusedForTheLock(t, err, time.Since(started), time.Hour)
}

// A row this module writes itself while another writer briefly holds the one
// connection of an in-memory database waits the hold out and is written: the
// bound ends a wait on the caller, not the queue a single connection makes of
// every statement on it.
func TestARowWrittenWhileAnotherWriterHoldsTheOneConnectionWaitsItOut(t *testing.T) {
	handle := openSingle(t)
	j := newTestJournal(handle, settings{})
	held, released := make(chan struct{}), make(chan error, 1)
	go func() {
		released <- handle.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&order{ID: "o1", Note: "held"}).Error; err != nil {
				return err
			}
			close(held)
			time.Sleep(briefHold)
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-released:
		t.Fatalf("the other writer's transaction ended before it held the connection: %v", err)
	}
	if err := j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"}); err != nil {
		t.Errorf("Record while another writer held the connection for %v gave %v, want it written once "+
			"the hold ended", briefHold, err)
	}
	if err := <-released; err != nil {
		t.Errorf("the other writer's transaction: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 2 {
		t.Errorf("the table holds %d row(s), want the held write's and the seam's", got)
	}
}

// errRowPanicked is what the callback in the case below panics with.
var errRowPanicked = errors.New("a callback panicked inside the audit row's transaction")

// A callback that panics inside the transaction GORM opens around a row this
// module writes, under a context that never ends, has the panic come out of
// the call, the row rolled back and the connection handed back. On an
// in-memory database held to one connection, the connection is the database:
// one left waiting on its own transaction would hold every later statement,
// and one dropped from the pool would take the tables with it.
func TestAPanicWhileARowIsWrittenHandsTheConnectionBack(t *testing.T) {
	handle := openSingle(t)
	j := newTestJournal(handle, settings{})
	var armed atomic.Bool
	if err := handle.Callback().Create().After("gorm:create").Before("gorm:commit_or_rollback_transaction").
		Register("test:panic-inside-the-row", func(tx *gorm.DB) {
			if armed.Load() && tx.Statement.Table == tableName {
				panic(errRowPanicked)
			}
		}); err != nil {
		t.Fatalf("registering the callback that panics: %v", err)
	}
	armed.Store(true)
	came := make(chan any, 1)
	go func() {
		defer func() { came <- recover() }()
		_ = j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"})
	}()
	select {
	case got := <-came:
		if err, ok := got.(error); !ok || !errors.Is(err, errRowPanicked) {
			t.Fatalf("Record gave %v, want the callback's panic carried up", got)
		}
	case <-time.After(connectionTimer):
		t.Fatalf("Record was still handing its connection back %v after the callback panicked", connectionTimer)
	}
	armed.Store(false)
	if got := len(auditRows(t, handle)); got != 0 {
		t.Errorf("the table holds %d row(s) once the row's transaction panicked, want it rolled back", got)
	}
	if err := j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"}); err != nil {
		t.Errorf("Record once the connection was handed back gave %v, want it written", err)
	}
}

// The journal records over a handle the host opened itself with gorm.Open, as
// it does over the one the database module delivers: a row this module writes
// takes a connection of whatever pool the handle is on, and a statement's row
// rides the statement's own session.
func TestTheJournalWorksOverAHandleTheHostOpened(t *testing.T) {
	handle, err := gorm.Open(sqlitedriver.Open(filepath.Join(t.TempDir(), "host.db")),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a handle of the host's own: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(handle) })
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	applyMigrations(t, handle, auditDialect)
	j := newTestJournal(handle, settings{})
	if err := j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"}); err != nil {
		t.Fatalf("Record over the host's handle: %v", err)
	}
	if err := handle.Create(&order{ID: "o1", Note: "host"}).Error; err != nil {
		t.Fatalf("a write over the host's handle: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 2 {
		t.Errorf("the table holds %d row(s) over the host's handle, want the seam's and the statement's", got)
	}
}
