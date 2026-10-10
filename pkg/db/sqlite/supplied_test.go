package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

// These cases supply an instance for this subpackage's module, named with the
// constant the package exports, and drive it through the command tree the way a
// program does.

// hostDatabase is an implementation of db.Database that the host wrote: a handle
// and the engine behind it. It is not a product of this module's New.
type hostDatabase struct{ handle *gorm.DB }

func (d *hostDatabase) DB() *gorm.DB                       { return d.handle }
func (d *hostDatabase) Dialect() db.Dialect                { return sqlite.Dialect }
func (d *hostDatabase) BlindIndex(plaintext string) string { return plaintext }

func (d *hostDatabase) Open(context.Context, string) (*gorm.DB, error) {
	return nil, errors.New("the host's own implementation opens no further connection")
}

var _ db.Database = (*hostDatabase)(nil)

// catalog declares the migration the cases look for in the database: applied
// behind an instance the run accepts, absent behind one it refuses.
func catalog() core.Module {
	return core.Module{Name: "catalog", Resources: []any{db.Migrations{FS: fstest.MapFS{
		"sqlite/0001_create_widgets.sql": &fstest.MapFile{Data: []byte(createWidgets)},
	}}}}
}

// builtByNew builds this module's product the way the design directs a host that
// wants to supply one: by this module's own New, in a registry made for the
// purpose, over a configuration naming the database at dsn. The reading face is
// loaded from that configuration and supplied to the config module of the
// registry, which is where New takes it from.
func builtByNew(t *testing.T, dsn string) db.Database {
	t.Helper()
	module := sqlite.Module()
	reg := assembly(t, fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn), module)
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration the New reads: %v", err)
	}
	reg.Supply(reader, config.ModuleName)

	built, err := module.New(t.Context(), reg)
	if err != nil {
		t.Fatalf("running this module's own New over %q: %v", dsn, err)
	}
	database, ok := built.(db.Database)
	if !ok {
		t.Fatalf("this module's New built a %T, which does not deliver the database capability", built)
	}
	t.Cleanup(func() {
		if err := db.Close(database.DB()); err != nil {
			t.Errorf("releasing the pool of the instance built over %q: %v", dsn, err)
		}
	})
	return database
}

// execute runs the registry as one command that takes every registered module in
// and drives the whole lifecycle, and returns the capability the probe took up
// once the run has reached Init, which is after the migrations are applied. A
// startup that failed returns the error it ended with. A run that started is shut
// down when the case ends.
//
// The command line is the process's, and a test binary carries arguments of its
// own, so they are taken away while the tree reads them; the cases here
// therefore do not run in parallel.
func execute(t *testing.T, reg *core.Registry, p *probe) (db.Database, error) {
	t.Helper()
	previous := os.Args
	os.Args = []string{"sqlite.test"}

	app := config.NewApp(reg)
	app.Root().IncludeAll()
	app.Root().Do(core.StageServe, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx) }()

	var instance db.Database
	select {
	case err := <-done:
		os.Args = previous
		cancel()
		return nil, err
	case instance = <-p.delivered:
		os.Args = previous
	}
	select {
	case err := <-done:
		cancel()
		return nil, err
	case <-p.migrated:
	}
	t.Cleanup(func() {
		close(p.gate)
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the assembly shut down with an error: %v", err)
		}
	})
	return instance, nil
}

// TestASuppliedInstanceThisModulesNewBuiltIsMigratedAndDelivered supplies the
// instance this module's own New built, under the name this package exports, and
// runs the assembly over a configuration that names no locator at all. The
// instance comes from either place a host gets one: a New run in a registry of
// its own making, and the shared test fixture, which runs it the same way.
//
// A Prepare that ran for the module would have failed the startup on the missing
// locator, and a New would have opened a database nobody configured: the run
// getting to Init on the supplied database is what shows neither ran. The
// dependant takes up that instance, and the migration the catalog declares is
// applied through its handle.
func TestASuppliedInstanceThisModulesNewBuiltIsMigratedAndDelivered(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(t *testing.T, dsn string) db.Database
	}{
		{"a New run in a registry of the host's making", builtByNew},
		{"the instance the shared test fixture returns", func(t *testing.T, dsn string) db.Database {
			return dbtest.SQLite(t, dsn, nil)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			built := c.build(t, filepath.Join(t.TempDir(), "supplied.db"))

			p := newProbe()
			reg := assembly(t, `{}`, sqlite.Module(), catalog(), p.module())
			reg.Supply(built, sqlite.ModuleName)

			instance, err := execute(t, reg, p)
			if err != nil {
				t.Fatalf("an assembly over an instance this module's own New built did not start: %v", err)
			}
			if instance != built {
				t.Errorf("the dependant took up %v, want the instance that was supplied", instance)
			}
			if !built.DB().Migrator().HasTable("widgets") {
				t.Error("the migration the catalog declares was not applied through the supplied instance")
			}
		})
	}
}

// TestAForeignInstanceSuppliedForThisModuleFailsTheStartup supplies an
// implementation of db.Database the host wrote, under the name this package
// exports: the assembly fails with db.ErrForeignInstance, no migration has been
// applied to the database behind it, and the pool behind it is released by the
// rollback.
func TestAForeignInstanceSuppliedForThisModuleFailsTheStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	handle := open(t, path)

	p := newProbe()
	reg := assembly(t, `{}`, sqlite.Module(), catalog(), p.module())
	reg.Supply(&hostDatabase{handle: handle}, sqlite.ModuleName)

	_, err := execute(t, reg, p)
	if !errors.Is(err, db.ErrForeignInstance) {
		t.Fatalf("an assembly over an implementation of db.Database the host wrote ended with %v, "+
			"want db.ErrForeignInstance", err)
	}
	if errors.Is(err, db.ErrMigrationFailed) {
		t.Errorf("the refusal is also a migration failure, which sends the host to migration files "+
			"that have nothing wrong with them: %v", err)
	}

	reopened := open(t, path)
	for _, table := range []string{"widgets", "db_migrations"} {
		if reopened.Migrator().HasTable(table) {
			t.Errorf("table %q exists in the database behind the refused instance", table)
		}
	}
	pool, poolErr := handle.DB()
	if poolErr != nil {
		t.Fatalf("reaching the pool behind the refused instance: %v", poolErr)
	}
	if err := pool.PingContext(t.Context()); err == nil {
		t.Error("the pool behind the refused instance still answers after the startup failed, so " +
			"nothing released it")
	}
}
