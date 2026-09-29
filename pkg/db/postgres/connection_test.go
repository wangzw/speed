package postgres_test

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestConnectionBoundsTheWaitForAConnectionOfAFullPool runs the engine-neutral
// case on a handle whose pool the configuration holds to one connection. Every
// connection held by a caller waiting on a statement of its own is the same
// shape on a pool of any size.
func TestConnectionBoundsTheWaitForAConnectionOfAFullPool(t *testing.T) {
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{
		"dsn": pgtest.Acquire(t), "max-open-conns": 1, "max-idle-conns": 1,
	})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	dbsuite.ConnectionWait(t, capability.DB(), db.Connection)
}

// TestConnectionBoundsTheWaitForALock runs the engine-neutral lock case, where a
// second pool's transaction holds the row lock the statement needs. This engine
// waits for a lock for as long as the statement's context lasts, and the
// context the case gives it has no end.
func TestConnectionBoundsTheWaitForALock(t *testing.T) {
	dsn := pgtest.Acquire(t)
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{"dsn": dsn})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWait(t, capability.DB(), other, db.Connection)
}

// TestConnectionBoundsTheLockWaitsOfEveryStatement runs the engine-neutral case
// for two statements of fn's that each meet a lock, where a second pool's
// transaction holds the row lock they need.
func TestConnectionBoundsTheLockWaitsOfEveryStatement(t *testing.T) {
	dsn := pgtest.Acquire(t)
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{"dsn": dsn})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWaitAcrossStatements(t, capability.DB(), other, db.Connection)
}

// TestConnectionBoundsBothWaitsTogether runs the engine-neutral case for a
// statement that waited for part of its wait to get its connection and then
// meets a lock, on a handle whose pool the configuration holds to one
// connection.
func TestConnectionBoundsBothWaitsTogether(t *testing.T) {
	dsn := pgtest.Acquire(t)
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{
		"dsn": dsn, "max-open-conns": 1, "max-idle-conns": 1,
	})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	other, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(other) })
	dbsuite.LockWaitAfterConnectionWait(t, capability.DB(), other, db.Connection)
}

// TestConnectionHandsTheConnectionBackWithItsLockTimeout holds the bound to the
// connection Connection took: inside fn the session's lock_timeout is at most
// the wait, and the connection goes back to the pool with the one its locator
// gave it. The locator's is not the server's default of none, so a connection
// handed back with that default instead is told apart; one handed back with the
// bound would cut every later statement's lock wait short.
func TestConnectionHandsTheConnectionBackWithItsLockTimeout(t *testing.T) {
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{
		"dsn": pgtest.Acquire(t) + "&lock_timeout=7000", "max-open-conns": 1, "max-idle-conns": 1,
	})
	if err != nil {
		t.Fatalf("starting a host on this engine: %v", err)
	}
	handle := capability.DB()

	const wait = 2 * time.Second
	var during string
	if err := db.Connection(t.Context(), handle, wait, func(session *gorm.DB) error {
		return session.Raw("SELECT current_setting('lock_timeout')").Scan(&during).Error
	}); err != nil {
		t.Fatalf("reading lock_timeout inside Connection: %v", err)
	}
	if bound, err := time.ParseDuration(during); err != nil || bound <= 0 || bound > wait {
		t.Errorf("lock_timeout inside Connection is %q, want a bound of at most %v", during, wait)
	}

	var after string
	if err := handle.Raw("SELECT current_setting('lock_timeout')").Scan(&after).Error; err != nil {
		t.Fatalf("reading lock_timeout after Connection: %v", err)
	}
	if after != "7s" {
		t.Errorf("the connection went back with a lock_timeout of %q, want the 7s its locator gave it", after)
	}
}
