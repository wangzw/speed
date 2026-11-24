// Package postgres is the PostgreSQL implementation of the database capability
// pkg/db declares: it binds the driver, names the configuration namespace, and
// supplies the cross-process migration mutex its dialect owes the migration run.
package postgres

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
)

const (
	// ModuleName is this implementation's name in the registry, its identity in
	// startup diagnostics and enablement lookups. A host that imports both
	// engines names the module to say which one a supplied instance belongs to.
	ModuleName = "db.postgres"
	// ConfigNamespace is where this implementation's input items are mounted,
	// the module name, disjoint from every other implementation's.
	ConfigNamespace = ModuleName
)

// Dialect is the engine this subpackage delivers, and the name of the
// subdirectory a migration set keeps this engine's files in.
const Dialect = db.Postgres

const (
	// MigrationLockTimeoutKey is the input item bounding the wait for the
	// migration mutex, relative to ConfigNamespace. It is pkg/db's constant, not
	// a second spelling.
	MigrationLockTimeoutKey = db.MigrationLockTimeoutKey
	// DefaultMigrationLockTimeout is how long a replica waits for another to
	// finish applying migrations before giving up.
	DefaultMigrationLockTimeout = 5 * time.Minute
)

// Dialector binds the PostgreSQL driver to a connection locator, for pkg/db to
// open the handle with.
func Dialector(dsn string) gorm.Dialector { return pgdriver.Open(dsn) }

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

// constraintViolation classifies 23505 as a duplicated key and 23503 as a foreign
// key violation; every other refusal stays the engine's own error.
func constraintViolation(err error) error {
	var refusal *pgconn.PgError
	if !errors.As(err, &refusal) {
		return nil
	}
	switch refusal.Code {
	case uniqueViolation:
		return gorm.ErrDuplicatedKey
	case foreignKeyViolation:
		return gorm.ErrForeignKeyViolated
	default:
		return nil
	}
}
