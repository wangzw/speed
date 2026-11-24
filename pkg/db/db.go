// Package db provides database access: the capability modules take up, the
// resource types they declare migrations and plugins through, and the
// connection, plugin, encryption and migration logic the implementation
// subpackages share.
//
// The calling surface is GORM's *gorm.DB: this package wraps GORM rather than
// abstracting it.
package db

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Dialect names a database engine. The set is closed: each implementation
// subpackage fixes one value, and a new engine adds a value here alongside its
// subpackage.
//
// A value is also the name of the subdirectory a migration set keeps that
// engine's files in, so it has to stay a single path element.
type Dialect string

const (
	// Postgres is PostgreSQL, delivered by pkg/db/postgres.
	Postgres Dialect = "postgres"
	// SQLite is SQLite, delivered by pkg/db/sqlite.
	SQLite Dialect = "sqlite"
)

// Database is the capability this directory delivers, the handle coming out of
// it through DB. Take it up with core.Resolve[db.Database](reg) and declare the
// dependency as core.Requirement{Token: (*db.Database)(nil)}.
type Database interface {
	// DB returns the handle to the database the configuration names, with
	// every declared plugin and the encryption serializer installed.
	DB() *gorm.DB
	// Dialect reports which engine is behind the handle. Callers writing
	// native SQL branch on it, because placeholder syntax and function names
	// differ between engines.
	Dialect() Dialect
	// BlindIndex returns the deterministic digest of a plaintext, for a
	// column an equality query can match: a field encrypted with the
	// encrypted serializer cannot be matched by equality, so a model found by
	// such a field carries a second column holding this digest.
	//
	// The digest is computed over the plaintext as given, with no
	// normalisation, and the caller fills the column: a row written without it
	// is still written, but an equality query against that column finds
	// nothing instead of failing.
	//
	// It panics when the instance holds no encryption key.
	BlindIndex(plaintext string) string
	// Open establishes an additional connection to another database,
	// assembled like the delivered handle: same dialect, declared plugins,
	// encryption serializer and logger, and the same connection pool
	// parameters, except that a locator naming a database private to one
	// connection, such as SQLite's :memory:, gets a pool of one.
	//
	// It carries no migrations and this module does not hold it: the caller
	// closes it with Close in its own Close stage. A blank locator is refused
	// with ErrConnectFailed. The connection attempt is bound to ctx.
	Open(ctx context.Context, dsn string) (*gorm.DB, error)
}

// Close releases a handle's connection pool. It is reentrant: closing an already
// closed handle, or nil, reports no error.
func Close(handle *gorm.DB) error {
	if handle == nil {
		return nil
	}
	pool, err := handle.DB()
	if err != nil {
		return fmt.Errorf("db: the handle has no connection pool to close: %w", err)
	}
	if err := pool.Close(); err != nil {
		return fmt.Errorf("db: closing the connection pool failed: %w", err)
	}
	return nil
}
