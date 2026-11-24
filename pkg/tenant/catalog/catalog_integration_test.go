//go:build integration

package catalog

import (
	"testing"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// PostgreSQL joins the cross-dialect cases; Acquire fails the case when the
// server does not answer rather than skipping it.
func init() {
	shapeDialects = append(shapeDialects, handleDialect{name: "postgres", open: openShapesOnPostgres})
	filteredDialects = append(filteredDialects, handleDialect{name: "postgres", open: openFilteredPostgres})
}

func openShapesOnPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	return filteredShapes(t, pgdriver.Open(pgtest.Acquire(t)))
}

// The PostgreSQL driver binds arguments itself rather than through
// database/sql's converter, so a refusal carried as an argument has a second
// path to the statement.
func openUnfilteredPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(pgdriver.Open(pgtest.Acquire(t)),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the handle's pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := handle.Exec(ordersDDL).Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	return handle
}

func openFilteredPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	handle := openUnfilteredPostgres(t)
	if err := handle.Use(newFilter()); err != nil {
		t.Fatalf("installing the filter: %v", err)
	}
	return handle
}
