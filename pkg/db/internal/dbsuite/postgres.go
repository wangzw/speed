//go:build integration

package dbsuite

import (
	"testing"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// init contributes the PostgreSQL fixture, so every dialect-neutral case runs
// against this engine too. The build tag keeps it out of the unit run, which is
// SQLite alone; the integration run's fixture comes from the compose stack, and
// a case that cannot reach the server fails.
func init() {
	Contribute(func(t *testing.T) Fixture {
		t.Helper()
		return Fixture{Dialect: "postgres", Open: OpenPostgres}
	})
}

// OpenPostgres opens a handle on an empty PostgreSQL database and closes it when
// the test ends. Every call gets a database of its own on the shared server, and
// the logger is discarded for the reason the SQLite fixture discards it.
func OpenPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := pgtest.Acquire(t)
	handle, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening the PostgreSQL fixture: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the PostgreSQL fixture's connection pool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("closing the PostgreSQL fixture: %v", err)
		}
	})
	return handle
}
