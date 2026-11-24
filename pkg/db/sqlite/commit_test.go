package sqlite_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

// enforcingLocator is a database file whose connections enforce foreign keys,
// which SQLite does only when a connection turns them on.
func enforcingLocator(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name) + "?_pragma=foreign_keys(1)"
}

const enforcingMemory = ":memory:?_pragma=foreign_keys(1)"

// TestARefusedCommitLeavesTheConnectionOutsideTheTransaction runs the engine-neutral case on a
// database file.
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

// TestACommitRefusedForTheLockLeavesTheConnectionOutsideTheTransaction runs the case over the other
// refusal SQLite keeps a transaction open after.
func TestACommitRefusedForTheLockLeavesTheConnectionOutsideTheTransaction(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "locked-commit.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(DELETE)&_pragma=busy_timeout(100)"
	capability := deliver(t, dsn, `"max-open-conns":1`, `"max-idle-conns":1`)
	observer, err := capability.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("opening a second pool on the file: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(observer) })

	refusedForTheLock := func(t *testing.T, handle *gorm.DB) error {
		t.Helper()
		pool, err := observer.DB()
		if err != nil {
			t.Fatalf("reaching the second pool: %v", err)
		}
		reader, err := pool.Conn(t.Context())
		if err != nil {
			t.Fatalf("taking a connection of the second pool: %v", err)
		}
		defer func() { _ = reader.Close() }()
		if _, err := reader.ExecContext(t.Context(), "BEGIN"); err != nil {
			t.Fatalf("beginning the reader's transaction: %v", err)
		}
		var owners int64
		if err := reader.QueryRowContext(t.Context(), "SELECT count(*) FROM commit_owners").Scan(&owners); err != nil {
			t.Fatalf("reading inside the reader's transaction: %v", err)
		}

		var early error
		refused := handle.Transaction(func(tx *gorm.DB) error {
			early = tx.Exec("INSERT INTO commit_owners (id) VALUES (1)").Error
			return early
		})
		if early != nil {
			t.Fatalf("a statement of the transaction was refused before its commit: %v", early)
		}
		if refused != nil && !strings.Contains(refused.Error(), "SQLITE_BUSY") {
			t.Fatalf("the commit was refused for another reason than the reader's lock: %v", refused)
		}
		if _, err := reader.ExecContext(t.Context(), "COMMIT"); err != nil {
			t.Fatalf("ending the reader's transaction: %v", err)
		}
		return refused
	}
	dbsuite.RefusedCommitBy(t, capability.DB(), observer, refusedForTheLock)
}

// TestARefusedCommitLeavesAnInMemoryDatabaseOutsideTheTransaction runs it on an in- memory
// database.
func TestARefusedCommitLeavesAnInMemoryDatabaseOutsideTheTransaction(t *testing.T) {
	dbsuite.RefusedCommit(t, deliver(t, enforcingMemory).DB(), nil)
}

// TestARefusedCommitOnAnOpenedHandle runs it on an in-memory database opened with Open, which is
// assembled the way the delivered handle is.
func TestARefusedCommitOnAnOpenedHandle(t *testing.T) {
	capability := deliver(t, enforcingLocator(t, "delivered.db"))
	opened, err := capability.Open(t.Context(), enforcingMemory)
	if err != nil {
		t.Fatalf("opening a second connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(opened) })
	dbsuite.RefusedCommit(t, opened, nil)
}
