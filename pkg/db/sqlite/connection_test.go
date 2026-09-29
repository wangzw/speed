package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

// TestConnectionBoundsTheWaitForTheOneConnectionOfAnInMemoryDatabase runs the
// engine-neutral case on the handle this engine holds to one connection
// whatever the pool parameters say: the shape on which a module's statement,
// issued from inside its caller's transaction, waits for that caller.
func TestConnectionBoundsTheWaitForTheOneConnectionOfAnInMemoryDatabase(t *testing.T) {
	dbsuite.ConnectionWait(t, deliver(t, ":memory:").DB(), db.Connection)
}

// TestConnectionBoundsTheWaitForAConnectionOfAFullPool runs it on a database
// file whose pool the configuration holds to one connection.
func TestConnectionBoundsTheWaitForAConnectionOfAFullPool(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "full-pool.db")
	dbsuite.ConnectionWait(t, deliver(t, dsn, `"max-open-conns":1`, `"max-idle-conns":1`).DB(), db.Connection)
}

// TestConnectionBoundsTheWaitOnAHandleThisModuleDidNotOpen runs it on a handle
// a host opened itself with gorm.Open, whose pool it holds to one connection:
// Connection takes its connection from whatever pool a handle is on.
func TestConnectionBoundsTheWaitOnAHandleThisModuleDidNotOpen(t *testing.T) {
	handle, err := gorm.Open(sqlitedriver.Open(filepath.Join(t.TempDir(), "host.db")),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a handle of the host's own: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(handle) })
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the host's pool: %v", err)
	}
	pool.SetMaxOpenConns(1)
	dbsuite.ConnectionWait(t, handle, db.Connection)
}

// busyTimeout is the busy timeout the cases below give their locators: not the
// driver's default of five seconds, and far short of the wait they give
// Connection.
const busyTimeout = 500 * time.Millisecond

// TestAStatementThroughConnectionWaitsForALockForTheBusyTimeout runs the
// engine-neutral lock case on a database file whose locator sets the busy
// timeout, while a second pool's transaction holds the write lock: the
// statement waits for the lock for the busy timeout, whatever wait Connection
// was given.
func TestAStatementThroughConnectionWaitsForALockForTheBusyTimeout(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "locked.db")
	capability := deliver(t, dsn+"?_pragma=busy_timeout(500)")
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the file: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWait(t, capability.DB(), other, busyTimeout, db.Connection)
}

// TestConnectionLeavesTheBusyTimeoutTheLocatorSet holds the connection
// Connection takes to the busy timeout it was opened with, inside fn and once
// it is back in the pool. The locator's is not the driver's default, so a
// connection whose busy timeout was replaced by anything is told apart.
func TestConnectionLeavesTheBusyTimeoutTheLocatorSet(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "busy-timeout.db") + "?_pragma=busy_timeout(7000)"
	handle := deliver(t, dsn, `"max-open-conns":1`, `"max-idle-conns":1`).DB()

	var during int64
	if err := db.Connection(t.Context(), handle, 2*time.Second, func(session *gorm.DB) error {
		return session.Raw("PRAGMA busy_timeout").Scan(&during).Error
	}); err != nil {
		t.Fatalf("reading the busy timeout inside Connection: %v", err)
	}
	if during != 7000 {
		t.Errorf("the busy timeout inside Connection is %dms, want the 7000ms its locator gave the connection",
			during)
	}
	var after int64
	if err := handle.Raw("PRAGMA busy_timeout").Scan(&after).Error; err != nil {
		t.Fatalf("reading the busy timeout after Connection: %v", err)
	}
	if after != 7000 {
		t.Errorf("the connection went back with a busy timeout of %dms, want the 7000ms its locator gave it", after)
	}
}

// TestConnectionRunsFnOnATransactionItIsHanded runs the engine-neutral case on
// the handle this engine holds to one connection.
func TestConnectionRunsFnOnATransactionItIsHanded(t *testing.T) {
	dbsuite.OnATransaction(t, deliver(t, ":memory:").DB(), db.Connection)
}

// TestAPanicInFnHandsTheConnectionBack runs the engine-neutral case on an
// in-memory database, which is the one connection this engine holds its
// handle to: a connection dropped from the pool takes the database with it.
func TestAPanicInFnHandsTheConnectionBack(t *testing.T) {
	dbsuite.PanicReleasesTheConnection(t, deliver(t, ":memory:").DB(), db.Connection)
}

// TestANestedTransactionThroughConnectionRollsBackToItsSavepoint runs the
// engine-neutral case on a database file.
func TestANestedTransactionThroughConnectionRollsBackToItsSavepoint(t *testing.T) {
	dbsuite.SavepointRecovers(t, deliver(t, filepath.Join(t.TempDir(), "savepoint.db")).DB(), db.Connection)
}
