//go:build integration

package dbsuite

import (
	"testing"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// init contributes the PostgreSQL fixture, so that every dialect-neutral
// migration case runs against this engine too without any of them being
// touched.
//
// This file is the seam, and the build tag is why it is a file of its own: the
// unit run's suite is SQLite alone — PostgreSQL is absent from the list rather
// than skipped within it — while the integration run adds it. The cross-process
// behaviour PostgreSQL carries has no other cover, so the engine may not be
// dropped from a run that is meant to exercise it: there the fixture comes from
// the repository's compose stack, and a case that cannot reach the server
// fails.
func init() {
	Contribute(func(t *testing.T) Fixture {
		t.Helper()
		return Fixture{Dialect: "postgres", Open: OpenPostgres}
	})
}

// OpenPostgres opens a handle on an empty PostgreSQL database, and closes it
// when the test ends.
//
// Every call gets a database of its own on the shared server. The cases
// assert on which tables exist and on what the record table holds, and neither
// survives being shared with the packages go test runs in parallel or with make
// check's second test leg.
//
// The GORM logger is discarded for the reason the SQLite fixture discards it:
// several cases fail a migration on purpose, and the default logger prints each
// one as an error line that reads like a test failure.
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
