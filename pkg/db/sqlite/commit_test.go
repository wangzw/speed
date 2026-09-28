package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

// enforcingLocator is a database file whose connections enforce foreign keys,
// which SQLite does only when a connection turns them on.
func enforcingLocator(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name) + "?_pragma=foreign_keys(1)"
}

// enforcingMemory is an in-memory database whose connection enforces foreign
// keys. It is private to the one connection that opens it, so a handle on it
// is held to that connection.
const enforcingMemory = ":memory:?_pragma=foreign_keys(1)"

// TestARefusedCommitLeavesTheConnectionOutsideTheTransaction runs the
// engine-neutral case on a database file. The pool is held to one connection,
// so every statement after the refusal lands on the connection the refused
// transaction ran on, and a second pool on the file is what sees whether a
// write made there was committed.
func TestARefusedCommitLeavesTheConnectionOutsideTheTransaction(t *testing.T) {
	dsn := enforcingLocator(t, "refused-commit.db")
	capability := deliver(t, dsn, `"max-open-conns":1`, `"max-idle-conns":1`)
	observer, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the file: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(observer) })
	dbsuite.RefusedCommit(t, capability.DB(), observer)
}

// TestARefusedCommitLeavesAnInMemoryDatabaseOutsideTheTransaction runs it on
// an in-memory database. The one connection is the database, so a transaction
// left open on it would be read by every later statement on the handle, and no
// transaction could be begun on the handle again.
func TestARefusedCommitLeavesAnInMemoryDatabaseOutsideTheTransaction(t *testing.T) {
	dbsuite.RefusedCommit(t, deliver(t, enforcingMemory).DB(), nil)
}

// TestARefusedCommitOnAnOpenedHandle runs it on an in-memory database opened
// with Open, which is assembled the way the delivered handle is.
func TestARefusedCommitOnAnOpenedHandle(t *testing.T) {
	capability := deliver(t, enforcingLocator(t, "delivered.db"))
	opened, err := capability.Open(t.Context(), enforcingMemory)
	if err != nil {
		t.Fatalf("opening a second connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(opened) })
	dbsuite.RefusedCommit(t, opened, nil)
}
