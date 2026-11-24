// Package dbsuite hands the migration tests one database per dialect, so the
// same cases run against every engine an implementation subpackage supports, and
// carries the cases each engine's own tests run on the handle its module
// delivers.
//
// It does not import pkg/db: the migration engine's entry point is unexported,
// so the cases driving it live in pkg/db's own test package, and a package those
// tests import cannot import pkg/db back. A fixture names its dialect with a
// plain string for the same reason.
package dbsuite

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Fixture is one dialect's test environment.
type Fixture struct {
	// Dialect is the dialect's value, also the name of the subdirectory a
	// migration set keeps that engine's files in.
	Dialect string
	// Open hands back a handle on a database with nothing in it; every call
	// gets its own.
	Open func(t *testing.T) *gorm.DB
}

var contributed []func(t *testing.T) Fixture

// Contribute registers a fixture, normally from an init function.
func Contribute(fixture func(t *testing.T) Fixture) {
	contributed = append(contributed, fixture)
}

// Fixtures gives every dialect this build runs the suite on. PostgreSQL is
// contributed only by the integration build.
func Fixtures(t *testing.T) []Fixture {
	t.Helper()
	out := []Fixture{{Dialect: "sqlite", Open: OpenSQLite}}
	for _, contribute := range contributed {
		out = append(out, contribute(t))
	}
	return out
}

// OpenSQLite opens a handle on an empty SQLite database in a temporary
// directory, with its pool held to one connection, and closes it when the test
// ends. SQLite locks the whole file for a write, so a second connection would
// report a locked database instead of what the case is about. The logger is
// discarded because several cases fail a migration on purpose.
func OpenSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "migrations.db")
	handle, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening the SQLite fixture at %s: %v", path, err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the SQLite fixture's connection pool: %v", err)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("closing the SQLite fixture: %v", err)
		}
	})
	return handle
}
