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

const deliveredBusyTimeout = 1500 * time.Millisecond

// selfWaitTimer is how long a case lets a write run before it counts the write
// as waiting on itself: past the delivered connections' busy timeout and past
// openedRowWait, with room for a loaded machine.
func selfWaitTimer() time.Duration {
	return deliveredBusyTimeout + coretest.Scale(3*time.Second)
}

func connectionTimer() time.Duration {
	return connectionWait + coretest.Scale(3*time.Second)
}

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

func busyTimeoutOf(ctx context.Context, t *testing.T, conn *sql.Conn) int64 {
	t.Helper()
	var ms int64
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Errorf("reading a connection's busy timeout: %v", err)
	}
	return ms
}

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

func withBusyTimeout(dsn string) string {
	return fmt.Sprintf("%s?_pragma=busy_timeout(%d)", dsn, deliveredBusyTimeout.Milliseconds())
}

func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Discard})
}

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

// lockHeadroom is how far past the delivered connection's busy timeout a write may
// return: a fifth of it.
const lockHeadroom = 300 * time.Millisecond

// driverBusyTimeout is the busy timeout the SQLite driver applies when the locator
// sets none.
const driverBusyTimeout = 5 * time.Second

// requireRefusedForTheLock checks that a write meeting the delivered database's
// lock is refused with ErrWriteFailed and SQLite's refusal.
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

// A write on an Open connection inside the caller's delivered-handle
// transaction fails instead of waiting on the caller.
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
				limit := selfWaitTimer()
				returned, err := within(limit, func() error {
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
						"handle was still waiting after %v", limit)
					return errCaseFailed
				}
				if shape.lock {
					// The upper bound in requireRefusedForTheLock is the delivered busy
					// timeout plus lockHeadroom through GATE_TIME_SCALE alone, and the timer
					// around this write carries the race factor as well.
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

// A write on an Open connection fails once the delivered connection's busy
// timeout passes while another holds the whole database.
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
		limit := selfWaitTimer()
		returned, err := within(limit, func() error {
			return quietly(opened).WithContext(context.Background()).Create(&order{ID: "o1", Note: "elsewhere"}).Error
		})
		took := time.Since(started)
		release()
		if !returned {
			t.Errorf("a write on a connection built with Open while the delivered database was held was "+
				"still waiting after %v", limit)
			return errCaseFailed
		}
		requireRefusedForTheLock(t, err, took, driverBusyTimeout)
		requireBusyTimeoutsAsOpened(ctx, t, handle)
		return nil
	})
}

// A write on an Open connection onto the delivered database itself is recorded
// on the statement's session while that database is held.
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
		again, err := database.Open(ctx, file+"?_txlock=exclusive")
		if err != nil {
			t.Errorf("opening the delivered database a second time: %v", err)
			return errCaseFailed
		}
		defer func() { _ = db.Close(again) }()

		limit := selfWaitTimer()
		returned, err := within(limit, func() error {
			return quietly(again).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return tx.Create(&order{ID: "o1", Note: "held"}).Error
			})
		})
		if !returned {
			t.Errorf("a write in a transaction holding the delivered database, on a connection opened onto "+
				"it, was still waiting after %v", limit)
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

const briefHold = 200 * time.Millisecond

// A write on a connection built with Open while another writer briefly
// holds the delivered database waits the hold out and is recorded.
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

// A row written from inside a caller's transaction on an in-memory database fails
// within the connection bound.
func TestARowWrittenInsideTheCallersTransactionOnOneConnectionDoesNotWaitForIt(t *testing.T) {
	t.Run("the seam", func(t *testing.T) {
		handle := openSingle(t)
		j := newTestJournal(handle, settings{})
		limit := connectionTimer()
		returned, err := within(limit, func() error {
			return handle.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&order{ID: "o1", Note: "held"}).Error; err != nil {
					return err
				}
				return j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"})
			})
		})
		if !returned {
			t.Fatalf("Record inside the caller's transaction was still waiting after %v", limit)
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
		limit := connectionTimer()
		returned, err := within(limit, func() error {
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
			t.Fatalf("publishing inside the caller's transaction was still waiting after %v", limit)
		}
		if err != nil {
			t.Errorf("the publisher's transaction gave %v, want the event's row to fail without failing it", err)
		}
		if !strings.Contains(reported.String(), audit.ErrWriteFailed.Error()) {
			t.Errorf("the lost row was not reported through the failure signal: %q", reported.String())
		}
	})
}

// A row this module writes itself waits for the delivered connection's busy
// timeout, whatever connectionWait is, then fails.
func TestARowWrittenWhileTheDeliveredDatabaseIsHeldFailsOnceTheBusyTimeoutPasses(t *testing.T) {
	file := filepath.Join(t.TempDir(), "delivered.db")
	handle := quietly(dbtest.SQLite(t, withBusyTimeout(file), nil).DB())
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	applyMigrations(t, handle, auditDialect)
	j := newTestJournal(handle, settings{})
	// A wrong implementation applies the connection wait where the busy timeout
	// bounds the lock, so widening it makes that implementation need an hour.
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
	limit := connectionTimer()
	returned, err := within(limit, func() error {
		return j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"})
	})
	if !returned {
		t.Fatalf("Record while the delivered database was held was still waiting after %v", limit)
	}
	requireRefusedForTheLock(t, err, time.Since(started), time.Hour)
}

// A row this module writes itself outlives a brief hold on an in-memory
// database's one connection and is written.
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

var errRowPanicked = errors.New("a callback panicked inside the audit row's transaction")

// A callback panic inside the transaction around a row this module writes
// escapes the call, rolls the row back and returns the connection.
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
	limit := connectionTimer()
	select {
	case got := <-came:
		if err, ok := got.(error); !ok || !errors.Is(err, errRowPanicked) {
			t.Fatalf("Record gave %v, want the callback's panic carried up", got)
		}
	case <-time.After(limit):
		t.Fatalf("Record was still handing its connection back %v after the callback panicked", limit)
	}
	armed.Store(false)
	if got := len(auditRows(t, handle)); got != 0 {
		t.Errorf("the table holds %d row(s) once the row's transaction panicked, want it rolled back", got)
	}
	if err := j.Record(context.Background(), audit.Entry{Action: "export", Object: "orders"}); err != nil {
		t.Errorf("Record once the connection was handed back gave %v, want it written", err)
	}
}

// The journal records over a handle the host opened itself with
// gorm.Open, as it does over the one the database module delivers.
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
