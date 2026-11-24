// Package sqlite is the SQLite implementation of the database capability pkg/db
// declares: it binds the pure-Go driver, names the configuration namespace, and
// holds a locator that names a per-connection database to a single connection.
package sqlite

import (
	"errors"

	gosqlite "github.com/glebarez/go-sqlite"
	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

const (
	// ModuleName is this implementation's name in the registry, its identity in
	// startup diagnostics and enablement lookups. A host that imports both
	// engines names the module to say which one a supplied instance belongs to.
	ModuleName = "db.sqlite"
	// ConfigNamespace is where this implementation's input items are mounted,
	// the module name, disjoint from every other implementation's.
	ConfigNamespace = ModuleName
)

// Dialect is the engine this subpackage delivers, and the name of the
// subdirectory a migration set keeps this engine's files in.
const Dialect = db.SQLite

// Dialector binds the SQLite driver to a connection locator, for pkg/db to open
// the handle with.
//
// A locator that puts its connections in shared-cache mode is refused: awaiting
// a table lock there is bounded by neither the busy timeout nor a context.
func Dialector(dsn string) gorm.Dialector {
	dialector := &sqlitedriver.Dialector{DriverName: driverName, DSN: dsn}
	if sharesCache(dsn) {
		return refusedDialector{Dialector: dialector}
	}
	return dialector
}

var errSharedCache = errors.New("db: SQLite is not opened in shared-cache mode (a file: locator with " +
	"cache=shared): the driver waits for a table lock another connection of the cache holds until that " +
	"connection's transaction ends, and neither the busy timeout nor a context ends that wait. An " +
	"in-memory database of the process's own is :memory:, which this module holds to one connection; " +
	"a database several connections share is a file")

// refusedDialector fails at initialisation, before any connection is made.
type refusedDialector struct{ *sqlitedriver.Dialector }

func (refusedDialector) Initialize(*gorm.DB) error { return errSharedCache }

// sharesCache reads the locator the way the driver and SQLite do: a file: URI
// whose last cache parameter is shared, naming anything but a temporary
// database. Outside a file: URI the parameters belong to the driver.
func sharesCache(dsn string) bool {
	l, ok := readLocator(dsn)
	return ok && l.sharedCache && l.name != ""
}

// constraintViolation classifies UNIQUE, PRIMARY KEY and ROWID refusals as a
// duplicated key and FOREIGN KEY as a foreign key violation. The rowid is
// classified here because the driver's own GORM translation leaves it out. A
// foreign key is enforced only where the locator turns it on.
func constraintViolation(err error) error {
	var refusal *gosqlite.Error
	if !errors.As(err, &refusal) {
		return nil
	}
	switch refusal.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_ROWID:
		return gorm.ErrDuplicatedKey
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return gorm.ErrForeignKeyViolated
	default:
		return nil
	}
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for registries that do not
// inherit the process-level registrations. It supplies no migration mutex: this
// engine does not appear in multi-replica deployments.
func Module() core.Module {
	return db.NewModule(db.Spec{
		ModuleName:          ModuleName,
		ConfigNamespace:     ConfigNamespace,
		Dialect:             Dialect,
		Dialector:           Dialector,
		ConstraintViolation: constraintViolation,
		ConnectionScoped:    connectionScoped,
	})
}
