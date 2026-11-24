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

type hostDatabase struct{ handle *gorm.DB }

func (d *hostDatabase) DB() *gorm.DB                       { return d.handle }
func (d *hostDatabase) Dialect() db.Dialect                { return db.SQLite }
func (d *hostDatabase) BlindIndex(plaintext string) string { return plaintext }

func (d *hostDatabase) Open(context.Context, string) (*gorm.DB, error) {
	return nil, errors.New("the host's own implementation opens no further connection")
}

var _ db.Database = (*hostDatabase)(nil)

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

func widgetsTable() core.Module {
	return core.Module{Name: "catalog", Resources: []any{migrationSet(db.SQLite, map[string]string{
		"0001_widgets.sql": `CREATE TABLE widgets (id INTEGER PRIMARY KEY)`,
	})}}
}

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

// TestASuppliedInstanceTheModulesOwnNewBuiltIsMigratedAndDelivered covers the
// instance a host supplies after this module's own New built it.
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

// TestAForeignInstanceSuppliedForTheModuleFailsTheStartup is the other half, through the whole
// command tree: an implementation of Database the host wrote, supplied for the module.
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
