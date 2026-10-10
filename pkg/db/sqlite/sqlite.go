// Package sqlite is the SQLite implementation of the database capability pkg/db
// declares.
//
// It binds the SQLite driver, names the configuration namespace this engine's
// input items hang under, and delivers the capability exclusively. The
// connection, plugin, encryption and migration logic are pkg/db's, shared with
// every other implementation; what is dialect-specific stops at the driver, the
// namespace, how the driver reports a constraint refusal, which locators name a
// database private to one connection, which locators it does not open at all,
// and how a transaction begins.
//
// Those locators — an in-memory database, a temporary one — are one database
// per connection in SQLite, so a handle on one is held to a single connection
// rather than to the configured pool: over the pool, the migrations would land
// on one connection and a request on another would find no table. Such a
// handle queues its statements: while a transaction or an open result set
// holds the connection, another statement on the same handle waits for it,
// until its context ends or for good, so a statement inside a transaction goes
// through the transaction's handle. A module issuing statements of its own on
// such a handle, where its caller may be holding the connection, takes one
// through db.Connection, which bounds that wait.
//
// A statement waits for a lock another connection holds for as long as its
// connection's busy timeout: the driver gives every connection one of 5
// seconds, which a locator's _pragma=busy_timeout(n) replaces, and an ending
// context does not cut the wait short. Nothing in this subpackage or in pkg/db
// changes it.
//
// A transaction begins IMMEDIATE: it takes the database's write lock at its
// BEGIN, waiting for it there within the busy timeout, so one that reads before
// it writes waits out a concurrent writer as it would on PostgreSQL. SQLite's
// own default, DEFERRED, takes the lock at the first write and refuses it at
// once, without that wait, to a transaction that has read while another
// connection holds it. The locator's _txlock parameter names another mode for
// its transactions; one naming none that opens the database read-only
// (mode=ro) begins them DEFERRED, and so does every transaction begun
// read-only.
//
// A transaction runs on every locator whose connections cannot write, and a
// write in it is refused as every write there is. SQLite begins an IMMEDIATE
// or EXCLUSIVE transaction on a database it opened read-only — mode=ro,
// immutable=1, a file the process may not write — as a read transaction, and
// refuses one outright on a connection whose query_only is on, whether a
// locator's _pragma or a statement turned it on; on such a connection a
// transaction begins DEFERRED, whatever mode the locator names.
//
// It supplies no cross-process migration mutex, and this is where it differs
// from the PostgreSQL subpackage beyond the driver. A mutex orders replicas that
// reach the first migration at once, and several processes sharing one SQLite
// file is not a deployment shape this engine is offered in: a mutex here would
// guard against a risk the supported assemblies cannot reach. Leaving it out
// also keeps migration-lock-timeout out of this implementation's input items,
// so a host that sets it is told the key is not one this implementation accepts,
// rather than believing it changed something.
//
// The namespace is this engine's alone, and that is load-bearing rather than
// tidy. The configuration manifest is collected from every registered module,
// the ones this run does not enable included, so two implementations declaring
// input items on one path conflict as soon as a host imports both — and that
// conflict is raised while the manifest is being collected, which is before
// resolution runs, so clearing it is nothing resolution can do. Importing
// several implementations and naming one on the command tree is the intended
// shape, and separate namespaces are what make it work.
//
// A dialect has admission conditions to meet before it may carry migrations, and
// all of them are properties of the engine and its driver rather than of this
// code: it must put DDL inside a transaction, or a migration's execution and its
// record stop being atomic; and it must run a migration file holding several
// statements without pkg/db splitting the text, or the same migration set would
// stop working on a change of engine. The dialect-neutral suite holds both on
// every engine it runs on. The third condition — a cross-process mutex — is for
// dialects that appear in multi-replica deployments, which this one does not.
//
// The driver is the pure-Go one, so building a host on this engine needs no C
// toolchain and works with CGO_ENABLED=0.
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
	// ModuleName is this implementation's name in the registry: its identity
	// in the startup diagnostics and in an enablement lookup. It is also the name
	// a host gives Supply for an instance of this module: both engines' products
	// are one type, defined in pkg/db, so a host that imports both names the
	// module to say which of them an instance belongs to.
	ModuleName = "db.sqlite"
	// ConfigNamespace is where this implementation's input items are mounted
	// in the config data. It is the module name, and it is disjoint from
	// every other implementation's by construction.
	ConfigNamespace = ModuleName
)

// Dialect is the engine this subpackage delivers. Its value is also the name
// of the subdirectory a migration set keeps this engine's files in.
const Dialect = db.SQLite

// Dialector binds the SQLite driver to a connection locator, for pkg/db to open
// the handle with.
//
// The driver appears here and nowhere in the capability's surface, which is
// what lets a host move between engines without touching a line of the code
// that uses the database. Its connections are opened through the wrapper in
// transactions.go, which begins each transaction in the mode the locator gives
// (beginStatement) and rolls one back when SQLite refuses to commit it.
//
// A locator that puts its connections in SQLite's shared-cache mode is refused:
// the dialector returned for it fails to initialise, so nothing is opened on it,
// neither the delivered handle nor a connection built with Open. In that mode a
// connection waiting for a table lock another connection of the cache holds is
// held by the driver until that connection's transaction ends — through
// sqlite3_unlock_notify, which neither the busy timeout nor an ending context
// reaches — so nothing bounds that wait, and a statement waiting on its own
// caller's transaction, on another connection of the cache, waits for good.
func Dialector(dsn string) gorm.Dialector {
	dialector := &sqlitedriver.Dialector{DriverName: driverName, DSN: dsn}
	if sharesCache(dsn) {
		return refusedDialector{Dialector: dialector}
	}
	return dialector
}

// errSharedCache is why a locator in shared-cache mode is not opened. It names
// the mode rather than the locator, which may carry a path a host keeps out of
// its diagnostics.
var errSharedCache = errors.New("db: SQLite is not opened in shared-cache mode (a file: locator with " +
	"cache=shared): the driver waits for a table lock another connection of the cache holds until that " +
	"connection's transaction ends, and neither the busy timeout nor a context ends that wait. An " +
	"in-memory database of the process's own is :memory:, which this module holds to one connection; " +
	"a database several connections share is a file")

// refusedDialector is the driver's dialector for a locator this engine does not
// open: initialising it fails before any connection is made.
type refusedDialector struct{ *sqlitedriver.Dialector }

// Initialize refuses the locator.
func (refusedDialector) Initialize(*gorm.DB) error { return errSharedCache }

// sharesCache reports whether a locator puts its connections in SQLite's
// shared-cache mode, reading it as the driver and SQLite do (readLocator): a
// file: URI whose last cache parameter is shared, naming any database but a
// temporary one, which SQLite never shares. Outside a file: URI the parameters
// are the driver's, cut off before SQLite sees the name, so they share nothing.
func sharesCache(dsn string) bool {
	l, ok := readLocator(dsn)
	return ok && l.sharedCache && l.name != ""
}

// constraintViolation classifies a refusal this engine's driver reports by its
// extended result code: a UNIQUE or PRIMARY KEY constraint, or a rowid that is
// not unique, is a duplicated key, and a FOREIGN KEY constraint is a foreign key
// violation. A foreign key is only enforced on a connection that turned it on —
// the locator's _pragma=foreign_keys(1) — and on one that did not, the refusal
// never happens.
//
// The driver's own GORM translation leaves the rowid out, so a table without an
// INTEGER PRIMARY KEY that is written with an explicit rowid would report its
// duplicates as something else; that is why the classification is written here
// against the result codes rather than taken from it.
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

// init registers this module with the process registry, so a host that imports
// the package has this engine available as soon as its configuration names it.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
//
// It delivers Database exclusively, which is what makes a host that configures
// this engine and another one fail the startup instead of running on whichever
// of them resolution happened to leave standing — with the dependants' tables
// split across two databases and nobody told.
//
// No migration mutex is passed, and the timeout that would bound waiting for one
// with it: this engine does not appear in multi-replica deployments.
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
