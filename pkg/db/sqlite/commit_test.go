package sqlite_test

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db/sqlite"
)

// enforcingLocator is a database file whose connections enforce foreign keys,
// which SQLite does only when a connection turns them on.
func enforcingLocator(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name) + "?_pragma=foreign_keys(1)"
}

// TestARefusedCommitLeavesTheConnectionOutsideTheTransaction covers the COMMIT
// SQLite refuses — a deferred foreign key it finds violated — which SQLite
// answers by keeping the transaction open while database/sql hands the
// connection back to the pool.
//
// The pool is held to one connection, so every statement after the refusal
// lands on the connection the refused transaction ran on. What it writes has
// to be committed: another pool on the same file has to see it, and a
// transaction begun on the connection has to begin.
func TestARefusedCommitLeavesTheConnectionOutsideTheTransaction(t *testing.T) {
	dsn := enforcingLocator(t, "refused-commit.db")
	handle := deliver(t, dsn, `"max-open-conns":1`, `"max-idle-conns":1`).DB()
	for _, statement := range []string{
		"CREATE TABLE owners (id INTEGER PRIMARY KEY)",
		"CREATE TABLE pets (id INTEGER PRIMARY KEY, " +
			"owner_id INTEGER REFERENCES owners (id) DEFERRABLE INITIALLY DEFERRED)",
	} {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the tables: %v", err)
		}
	}

	err := handle.Transaction(func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO pets (id, owner_id) VALUES (1, 999)").Error
	})
	if err == nil {
		t.Fatal("the commit over a missing owner went through")
	}

	if written := handle.Exec("INSERT INTO owners (id) VALUES (1)").Error; written != nil {
		t.Fatalf("writing after the refused commit: %v", written)
	}
	other, err := gorm.Open(sqlite.Dialector(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening a second pool on the file: %v", err)
	}
	t.Cleanup(func() {
		if pool, err := other.DB(); err == nil {
			_ = pool.Close()
		}
	})
	var owners int64
	if err := other.Raw("SELECT count(*) FROM owners").Scan(&owners).Error; err != nil {
		t.Fatalf("counting from the second pool: %v", err)
	}
	if owners != 1 {
		t.Errorf("another pool sees %d owners after a write that reported success, want 1: the write "+
			"went into the transaction the refused commit left open", owners)
	}

	if err := handle.Transaction(func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO pets (id, owner_id) VALUES (2, 1)").Error
	}); err != nil {
		t.Errorf("a transaction begun after the refused commit failed: %v", err)
	}
}
