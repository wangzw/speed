package journal

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// selfWaitTimer is how long a case lets a write run before it counts the write
// as waiting on itself. It is past the bound a row of a statement on a
// connection built with Open waits within, with room for a loaded machine, and
// short of the busy timeout a file database would otherwise make the row wait
// for: the driver's default, or the longer one deliveredBusyTimeout sets.
const selfWaitTimer = 4 * time.Second

// connectionTimer is the same for a row this module writes itself, which waits
// for a connection within connectionWait.
const connectionTimer = connectionWait + 3*time.Second

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

// requireBusyTimeoutsHandedBack checks every idle connection of the handle's
// pool for the busy timeout the pool's connections carry when they are opened.
// A row for a statement on a connection built with Open shortens that timeout on
// the connection it takes, and a connection handed back still carrying it would
// make every later writer on it give up on the lock within that bound.
func requireBusyTimeoutsHandedBack(ctx context.Context, t *testing.T, handle *gorm.DB, want int64) {
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
		if got := busyTimeoutOf(ctx, t, conn); got != want {
			t.Errorf("a connection of the delivered pool was handed back with a busy timeout of %dms, want "+
				"the %dms it was opened with", got, want)
		}
	}
}

// openedBusyTimeout reads the busy timeout the delivered pool opens its
// connections with.
func openedBusyTimeout(ctx context.Context, t *testing.T, handle *gorm.DB) int64 {
	t.Helper()
	pool, err := handle.DB()
	if err != nil {
		t.Errorf("reaching the delivered pool: %v", err)
		return 0
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Errorf("taking a connection: %v", err)
		return 0
	}
	defer func() { _ = conn.Close() }()
	return busyTimeoutOf(ctx, t, conn)
}

// deliveredBusyTimeout is the busy timeout, in milliseconds, the delivered
// locator sets in the cases that check it is handed back. It is not the
// driver's default, so a connection handed back carrying the default instead of
// what it was opened with is seen; and it is longer than selfWaitTimer, so a row
// whose lock wait was left to it is seen waiting.
const deliveredBusyTimeout = 7000

// withBusyTimeout is a SQLite locator that opens every connection with
// deliveredBusyTimeout.
func withBusyTimeout(dsn string) string {
	return fmt.Sprintf("%s?_pragma=busy_timeout(%d)", dsn, deliveredBusyTimeout)
}

// requireOpenedWithTheLocatorsBusyTimeout checks that the delivered pool opens
// its connections with the busy timeout the case's locator set, and returns it.
// A pool that opened them with the default would leave a hand-back that
// restores the default indistinguishable from one that restores what it read.
func requireOpenedWithTheLocatorsBusyTimeout(ctx context.Context, t *testing.T, handle *gorm.DB) int64 {
	t.Helper()
	busy := openedBusyTimeout(ctx, t, handle)
	if busy != deliveredBusyTimeout {
		t.Errorf("the delivered pool opens its connections with a busy timeout of %dms, want the %dms the "+
			"locator sets", busy, deliveredBusyTimeout)
	}
	return busy
}

// quietly is a session that does not print the statement a case fails on
// purpose.
func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Discard})
}

// requireRefusedForWaiting checks that a write on a connection built with Open
// was refused for its record, and that the refusal names the wait.
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

// A write on a connection built with Open inside a transaction the caller holds
// on the delivered handle fails within its row's bound rather than waiting on
// the caller.
//
// The row goes to the delivered database, and what it needs there is held by
// the caller until after the write returns: the write lock on a file database,
// the one connection on an in-memory one. Waiting for either is waiting for the
// caller itself — until the busy timeout on a file, for good on an in-memory
// database when the context carries no deadline, which none of these does.
//
// The row shortens the busy timeout of the delivered connection it takes, and
// the locators set one of their own, so a connection handed back with anything
// but what it carried is seen.
func TestAWriteOnAnOpenedConnectionInsideTheCallersTransactionDoesNotWaitOnIt(t *testing.T) {
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
				busy := requireOpenedWithTheLocatorsBusyTimeout(ctx, t, handle)

				returned, err := within(selfWaitTimer, func() error {
					return quietly(handle).Transaction(func(tx *gorm.DB) error {
						if heldErr := tx.Create(&order{ID: "d1", Note: "delivered"}).Error; heldErr != nil {
							return heldErr
						}
						return quietly(opened).WithContext(context.Background()).
							Create(&order{ID: "o1", Note: "elsewhere"}).Error
					})
				})
				if !returned {
					t.Errorf("a write on a connection built with Open inside a transaction on the delivered "+
						"handle was still waiting after %v", selfWaitTimer)
					return errCaseFailed
				}
				requireRefusedForWaiting(t, err)
				var committed int64
				if err := opened.Model(&order{}).Count(&committed).Error; err != nil || committed != 0 {
					t.Errorf("the write was refused, but %d order(s) committed on its database (%v)", committed, err)
				}
				requireBusyTimeoutsHandedBack(ctx, t, handle, busy)
				return nil
			})
		})
	}
}

// briefHold is how long another writer holds the delivered database in the
// case below: far longer than an insert takes, well inside the bound.
const briefHold = 200 * time.Millisecond

// A write on a connection built with Open while another writer briefly holds
// the delivered database waits the hold out and is recorded.
//
// The bound is on waiting, not a refusal to wait: a row that gave up the moment
// the delivered database was busy would fail every such write whenever any
// other request happened to be writing there.
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
				busy := requireOpenedWithTheLocatorsBusyTimeout(ctx, t, handle)

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
				requireBusyTimeoutsHandedBack(ctx, t, handle, busy)
				return nil
			})
		})
	}
}

// openSingle builds a handle on an in-memory database held to one connection,
// the shape the database module gives a locator private to its connection,
// with the capture plugin installed and both tables created.
func openSingle(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(sqlitedriver.Open(":memory:"), quiet())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the pool: %v", err)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
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
// The file database fails the same write when the driver's busy timeout ends
// the wait for the caller's lock; the in-memory database's one connection plays
// the lock's part and nothing else ends that wait. The event path drops the
// publisher's cancellation, so there not even a deadline would.
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
