//go:build integration

package postgres_test

import (
	"database/sql"
	"errors"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestPostgresPutsDDLInsideATransaction holds this engine to the first
// admission condition the design sets for an implementation subpackage.
//
// A migration's execution and its record are written in one transaction, so
// that "executed but not recorded" cannot happen. That rests entirely on the
// engine rolling DDL back with the rest of the transaction. On an engine where
// it does not, the CREATE TABLE survives while the record rolls away, the next
// startup runs it again, and it fails on an object that already exists — one
// startup after the one that caused it.
func TestPostgresPutsDDLInsideATransaction(t *testing.T) {
	handle := open(t, pgtest.Acquire(t))

	rollback := errors.New("rolling this transaction back on purpose")
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE TABLE rolled_back (id INTEGER PRIMARY KEY)`).Error; err != nil {
			return err
		}
		if !tableExists(t, tx, "rolled_back") {
			// Without this the case would pass on an engine that
			// refused the statement outright, which is a different
			// thing from rolling it back.
			t.Error("the table is not there inside the transaction that created it")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("the transaction reported %v, and the rollback this case turns on did not happen", err)
	}
	if tableExists(t, handle, "rolled_back") {
		t.Error("the table created inside the rolled-back transaction is still there, so this engine " +
			"keeps DDL outside the transaction and a migration's execution and its record are not atomic")
	}
}

// open opens a handle on a database this package's fixtures handed back, using
// the dialector this subpackage exports, and closes it when the test ends.
//
// The GORM logger is discarded: several cases fail a statement on purpose, and
// the default logger prints each one as an error line that reads like a test
// failure.
func open(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(postgres.Dialector(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening the PostgreSQL fixture: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the fixture's connection pool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("closing the fixture: %v", err)
		}
	})
	return handle
}

// tableExists reports whether a table is visible to the handle.
func tableExists(t *testing.T, handle *gorm.DB, table string) bool {
	t.Helper()
	var name sql.NullString
	if err := handle.Raw(`SELECT to_regclass(?)::text`, table).Scan(&name).Error; err != nil {
		t.Fatalf("asking whether the table %q exists: %v", table, err)
	}
	return name.Valid
}
