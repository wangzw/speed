package postgres_test

import (
	"context"
	"testing"
	"time"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// deliveredOnOneConnection starts a host on this engine over a database of the
// fixture's, with the pool held to one connection.
func deliveredOnOneConnection(t *testing.T) *gorm.DB {
	t.Helper()
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{
		"dsn": pgtest.Acquire(t), "max-open-conns": 1, "max-idle-conns": 1,
	})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	return capability.DB()
}

// TestConnectionBoundsTheWaitForAConnectionOfAFullPool runs the engine-neutral
// case on a handle whose pool the configuration holds to one connection. Every
// connection held by a caller waiting on a statement of its own is the same
// shape on a pool of any size.
func TestConnectionBoundsTheWaitForAConnectionOfAFullPool(t *testing.T) {
	dbsuite.ConnectionWait(t, deliveredOnOneConnection(t), db.Connection)
}

// TestConnectionBoundsTheWaitOnAHandleThisModuleDidNotOpen runs it on a handle
// a host opened itself with gorm.Open, whose pool it holds to one connection:
// Connection takes its connection from whatever pool a handle is on.
func TestConnectionBoundsTheWaitOnAHandleThisModuleDidNotOpen(t *testing.T) {
	handle, err := gorm.Open(pgdriver.Open(pgtest.Acquire(t)), &gorm.Config{Logger: logger.Discard})
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

// lockTimeout is the lock_timeout the case below gives its locator: far short
// of the wait it gives Connection, and something where the server's default is
// none.
const lockTimeout = 500 * time.Millisecond

// TestAStatementThroughConnectionWaitsForALockForTheLockTimeout runs the
// engine-neutral lock case where the locator sets the session's lock_timeout
// and a second pool's transaction holds the row lock the statement needs: the
// statement waits for the lock for the lock_timeout, whatever wait Connection
// was given.
func TestAStatementThroughConnectionWaitsForALockForTheLockTimeout(t *testing.T) {
	dsn := pgtest.Acquire(t)
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{"dsn": dsn + "&lock_timeout=500"})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWait(t, capability.DB(), other, lockTimeout, db.Connection)
}

// TestConnectionLeavesTheLockTimeoutTheLocatorSet holds the connection
// Connection takes to the session's lock_timeout, inside fn and once it is back
// in the pool. The locator's is not the server's default of none, so a
// connection whose lock_timeout was replaced by anything is told apart.
func TestConnectionLeavesTheLockTimeoutTheLocatorSet(t *testing.T) {
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{
		"dsn": pgtest.Acquire(t) + "&lock_timeout=7000", "max-open-conns": 1, "max-idle-conns": 1,
	})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	handle := capability.DB()

	var during string
	if err := db.Connection(t.Context(), handle, 2*time.Second, func(session *gorm.DB) error {
		return session.Raw("SELECT current_setting('lock_timeout')").Scan(&during).Error
	}); err != nil {
		t.Fatalf("reading lock_timeout inside Connection: %v", err)
	}
	if during != "7s" {
		t.Errorf("lock_timeout inside Connection is %q, want the 7s its locator gave the session", during)
	}
	var after string
	if err := handle.Raw("SELECT current_setting('lock_timeout')").Scan(&after).Error; err != nil {
		t.Fatalf("reading lock_timeout after Connection: %v", err)
	}
	if after != "7s" {
		t.Errorf("the connection went back with a lock_timeout of %q, want the 7s its locator gave it", after)
	}
}

// TestConnectionRunsFnOnATransactionItIsHanded runs the engine-neutral case on
// a handle whose pool the configuration holds to one connection.
func TestConnectionRunsFnOnATransactionItIsHanded(t *testing.T) {
	dbsuite.OnATransaction(t, deliveredOnOneConnection(t), db.Connection)
}

// TestAPanicInFnHandsTheConnectionBack runs the engine-neutral case on a handle
// whose pool the configuration holds to one connection.
func TestAPanicInFnHandsTheConnectionBack(t *testing.T) {
	dbsuite.PanicReleasesTheConnection(t, deliveredOnOneConnection(t), db.Connection)
}

// TestANestedTransactionThroughConnectionRollsBackToItsSavepoint runs the
// engine-neutral case: this engine accepts nothing in a transaction a statement
// failed in but the rollback to its savepoint.
func TestANestedTransactionThroughConnectionRollsBackToItsSavepoint(t *testing.T) {
	dbsuite.SavepointRecovers(t, deliveredOnOneConnection(t), db.Connection)
}
