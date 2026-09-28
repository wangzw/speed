package journal

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// pgMigration is the migration the module ships for PostgreSQL. The cases below
// build the audit table from it, so the file itself is what they exercise.
const pgMigration = "postgres/0001_audit_entries.sql"

// openPostgres builds a handle on a database of this test's own on the shared
// PostgreSQL fixture, with the capture plugin installed, the orders table
// created and the module's own PostgreSQL migration applied.
//
// These are the cases the in-memory handle cannot stand in for: a statement
// that fails inside a PostgreSQL transaction leaves that transaction unable to
// run anything else, which is a property of the engine and not of the plugin.
func openPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(pgdriver.Open(pgtest.Acquire(t)), quiet())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	t.Cleanup(func() {
		if pool, err := handle.DB(); err == nil {
			_ = pool.Close()
		}
	})
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	applyMigration(t, handle, pgMigration)
	return handle
}

// A duplicate key on PostgreSQL reaches the caller as the engine's own error.
//
// The engine refuses every further statement in a transaction one has failed
// in, so a record attempted for the failed statement cannot be written there at
// all; attempting it only adds a second failure to the caller's error.
func TestPostgresDuplicateKeyKeepsTheEnginesError(t *testing.T) {
	handle := openPostgres(t)
	if err := handle.Create(&order{ID: "o1", Note: "first"}).Error; err != nil {
		t.Fatalf("writing the order: %v", err)
	}

	err := handle.Create(&order{ID: "o1", Note: "again"}).Error
	var engine *pgconn.PgError
	if !errors.As(err, &engine) || engine.Code != "23505" {
		t.Fatalf("the duplicate gave %v, want the engine's unique violation", err)
	}
	if errors.Is(err, audit.ErrWriteFailed) || strings.Contains(err.Error(), audit.ErrWriteFailed.Error()) {
		t.Fatalf("a duplicate key was reported as an audit write failure: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 1 {
		t.Fatalf("want only the successful write recorded, got %d row(s)", got)
	}
}
