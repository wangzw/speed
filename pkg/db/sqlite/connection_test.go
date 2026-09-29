package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

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

// TestConnectionBoundsTheWaitForALock runs the engine-neutral lock case on a
// database file, where a second pool's transaction holds the write lock. The
// driver's default busy timeout of five seconds is what the statement would
// wait otherwise, whatever its context.
func TestConnectionBoundsTheWaitForALock(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "locked.db")
	capability := deliver(t, dsn)
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the file: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWait(t, capability.DB(), other, db.Connection)
}

// TestConnectionBoundsTheLockWaitsOfEveryStatement runs the engine-neutral case
// for two statements of fn's that each meet a lock, on a database file where a
// second pool's transaction holds the write lock.
func TestConnectionBoundsTheLockWaitsOfEveryStatement(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "every-statement.db")
	capability := deliver(t, dsn)
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the file: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWaitAcrossStatements(t, capability.DB(), other, db.Connection)
}

// TestConnectionBoundsBothWaitsTogether runs the engine-neutral case for a
// statement that waited for part of its wait to get its connection and then
// meets a lock, on a database file whose pool the configuration holds to one
// connection.
func TestConnectionBoundsBothWaitsTogether(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "both-waits.db")
	capability := deliver(t, dsn, `"max-open-conns":1`, `"max-idle-conns":1`)
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the file: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWaitAfterConnectionWait(t, capability.DB(), other, db.Connection)
}

// TestConnectionHandsTheConnectionBackWithItsBusyTimeout holds the bound to the
// connection Connection took: inside fn the busy timeout is at most the wait,
// and the connection goes back to the pool with the one its locator gave it.
// The locator's is not the driver's default, so a connection handed back with
// that default instead is told apart; one handed back with the bound would cut
// every later statement's lock wait short.
func TestConnectionHandsTheConnectionBackWithItsBusyTimeout(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "busy-timeout.db") + "?_pragma=busy_timeout(7000)"
	handle := deliver(t, dsn, `"max-open-conns":1`, `"max-idle-conns":1`).DB()

	const wait = 2 * time.Second
	var during int64
	if err := db.Connection(t.Context(), handle, wait, func(session *gorm.DB) error {
		return session.Raw("PRAGMA busy_timeout").Scan(&during).Error
	}); err != nil {
		t.Fatalf("reading the busy timeout inside Connection: %v", err)
	}
	if during <= 0 || during > wait.Milliseconds() {
		t.Errorf("the busy timeout inside Connection is %dms, want a bound of at most %v", during, wait)
	}

	var after int64
	if err := handle.Raw("PRAGMA busy_timeout").Scan(&after).Error; err != nil {
		t.Fatalf("reading the busy timeout after Connection: %v", err)
	}
	if after != 7000 {
		t.Errorf("the connection went back with a busy timeout of %dms, want the 7000ms its locator gave it", after)
	}
}
