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

// TestPostgresPutsDDLInsideATransaction holds this engine to the first admission condition the
// an implementation subpackage has to meet.
func TestPostgresPutsDDLInsideATransaction(t *testing.T) {
	handle := open(t, pgtest.Acquire(t))

	rollback := errors.New("rolling this transaction back on purpose")
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE TABLE rolled_back (id INTEGER PRIMARY KEY)`).Error; err != nil {
			return err
		}
		if !tableExists(t, tx, "rolled_back") {
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

func tableExists(t *testing.T, handle *gorm.DB, table string) bool {
	t.Helper()
	var name sql.NullString
	if err := handle.Raw(`SELECT to_regclass(?)::text`, table).Scan(&name).Error; err != nil {
		t.Fatalf("asking whether the table %q exists: %v", table, err)
	}
	return name.Valid
}
