package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// These cases take the instance a host supplies for a database module through
// the command tree, the way a program meets it: the instance is supplied to the
// registry the tree is built over, and Execute drives it.

// hostDatabase is an implementation of Database that the host wrote: a handle
// and the engine behind it. It is not a product of the module's New.
type hostDatabase struct{ handle *gorm.DB }

func (d *hostDatabase) DB() *gorm.DB                       { return d.handle }
func (d *hostDatabase) Dialect() db.Dialect                { return db.SQLite }
func (d *hostDatabase) BlindIndex(plaintext string) string { return plaintext }

func (d *hostDatabase) Open(context.Context, string) (*gorm.DB, error) {
	return nil, errors.New("the host's own implementation opens no further connection")
}

var _ db.Database = (*hostDatabase)(nil)

// plainHandle opens the SQLite database at path the way a host that wrote its
// own Database does: a plain GORM handle, with none of what the module installs
// on the ones it opens. The pool is released when the case ends, whether or not
// the run under test released it first.
func plainHandle(t *testing.T, path string) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening the SQLite database at %s: %v", path, err)
	}
	pool := poolOf(t, handle)
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("releasing the connection pool at %s: %v", path, err)
		}
	})
	return handle
}

// widgetsTable is the module that declares the migration the cases look for in
// the database: applied behind an instance the run accepts, absent behind one it
// refuses.
func widgetsTable() core.Module {
	return core.Module{Name: "catalog", Resources: []any{migrationSet(db.SQLite, map[string]string{
		"0001_widgets.sql": `CREATE TABLE widgets (id INTEGER PRIMARY KEY)`,
	})}}
}

// builtByNew builds a product of sqliteSpec's module the way the design directs
// a host that wants to supply one: by that module's own New, in a registry made
// for the purpose, over a configuration naming the database at dsn. The reading
// face is loaded from that configuration and supplied to the config module of
// the registry, which is where New takes it from.
func builtByNew(t *testing.T, dsn string) db.Database {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "built.json")
	body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}}}`, dsn)
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary configuration source: %v", err)
	}

	module := db.NewModule(sqliteSpec())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDDBTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(module)
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration the New reads: %v", err)
	}
	reg.Supply(reader, config.ModuleName)

	built, err := module.New(t.Context(), reg)
	if err != nil {
		t.Fatalf("running the module's own New over %s: %v", dsn, err)
	}
	database, ok := built.(db.Database)
	if !ok {
		t.Fatalf("the module's New built a %T, which does not deliver the database capability", built)
	}
	t.Cleanup(func() {
		if err := db.Close(database.DB()); err != nil {
			t.Errorf("releasing the pool of the instance built over %s: %v", dsn, err)
		}
	})
	return database
}

// startWithForeignInstance starts a host whose database module was supplied an
// implementation of Database the host wrote, over a database of its own, and
// returns the handle that implementation carries, the path of that database and
// what the startup ended with.
func startWithForeignInstance(t *testing.T) (*gorm.DB, string, error) {
	t.Helper()
	spec := sqliteSpec()
	path := filepath.Join(t.TempDir(), "foreign.db")
	handle := plainHandle(t, path)
	stub := &hostDatabase{handle: handle}
	_, err := startHostWith(t, spec, `{}`, func(reg *core.Registry) {
		reg.Supply(stub, spec.ModuleName)
	}, widgetsTable())
	return handle, path, err
}

// TestASuppliedInstanceTheModulesOwnNewBuiltIsMigratedAndDelivered is the path
// the design leaves a host that wants to supply a database: the instance is
// built by the module's own New and supplied for the module, and the run treats
// it as the module's own.
//
// The configuration the run reads names no locator, so a Prepare or a New that
// ran for the module would have failed the startup on the missing key: what is
// observed is that neither did. The dependant takes up the instance that was
// supplied and not another, the migration the catalog declares is applied through
// its handle, and the run's shutdown ends without an error, which is the pool
// behind the instance being released by the drive.
func TestASuppliedInstanceTheModulesOwnNewBuiltIsMigratedAndDelivered(t *testing.T) {
	spec := sqliteSpec()
	built := builtByNew(t, filepath.Join(t.TempDir(), "built.db"))

	h, err := startHostWith(t, spec, `{}`, func(reg *core.Registry) {
		reg.Supply(built, spec.ModuleName)
	}, widgetsTable())
	if err != nil {
		t.Fatalf("starting a host over an instance the module's own New built: %v", err)
	}
	if h.Instance != built {
		t.Errorf("the dependant took up %v, want the instance that was supplied", h.Instance)
	}
	if !built.DB().Migrator().HasTable("widgets") {
		t.Error("the migration the catalog declares was not applied through the supplied instance")
	}
	h.finish(t)
	if err := poolOf(t, built.DB()).PingContext(t.Context()); err == nil {
		t.Error("the pool behind the supplied instance still answers after the host shut down, so " +
			"nothing released it")
	}
}

// TestAForeignInstanceSuppliedForTheModuleFailsTheStartup is the other half,
// through the whole command tree: an implementation of Database the host wrote,
// supplied for the module, fails the startup with ErrForeignInstance and with no
// other class.
//
// What it leaves behind is looked at in the database itself, reopened afterwards:
// neither the migration nor the record table is there. And the pool behind the
// instance is released, by the rollback of the drive that refused it.
func TestAForeignInstanceSuppliedForTheModuleFailsTheStartup(t *testing.T) {
	handle, path, err := startWithForeignInstance(t)
	assertReportsOnly(t, err, "ErrForeignInstance")

	reopened := plainHandle(t, path)
	for _, table := range []string{"widgets", "db_migrations"} {
		if reopened.Migrator().HasTable(table) {
			t.Errorf("table %q exists in the database behind the refused instance", table)
		}
	}
	if err := poolOf(t, handle).PingContext(t.Context()); err == nil {
		t.Error("the pool behind the refused instance still answers after the startup failed, so " +
			"nothing released it")
	}
}
