package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/db/postgres"
)

var sharedItems = []string{
	"dsn",
	"max-open-conns",
	"max-idle-conns",
	"conn-max-lifetime",
	"encryption-key",
	"encryption-retired-keys",
}

// TestPostgresModuleShape holds the descriptor this subpackage registers to the
// shape of an implementation module.
func TestPostgresModuleShape(t *testing.T) {
	module := postgres.Module()

	if module.Name != "db.postgres" {
		t.Errorf("the module registers as %q, and the design names it db.postgres", module.Name)
	}
	if module.Name != postgres.ModuleName {
		t.Errorf("the registered name %q and the exported constant %q differ, and the startup "+
			"diagnostics name the module through the former", module.Name, postgres.ModuleName)
	}

	if len(module.Provides) != 1 {
		t.Fatalf("the module declares %d capabilities, and an implementation delivers exactly the "+
			"database capability", len(module.Provides))
	}
	provision := module.Provides[0]
	if want := reflect.TypeOf((*db.Database)(nil)); reflect.TypeOf(provision.Token) != want {
		t.Errorf("the module delivers %v, and the capability is %v", reflect.TypeOf(provision.Token), want)
	}
	if !provision.Exclusive {
		t.Error("the module does not claim the database capability exclusively, so a host that " +
			"configured two engines would get one of them silently and find half its tables in a " +
			"database nobody mentioned")
	}
	dbsuite.RequireOptionalLogging(t, "the module this subpackage builds", module)

	schema := schemaOf(t, module)
	if schema.Namespace != "db.postgres" {
		t.Errorf("the items are mounted on %q, and this implementation's own section is db.postgres",
			schema.Namespace)
	}
	if schema.Namespace != postgres.ConfigNamespace {
		t.Errorf("the schema is mounted on %q and the exported namespace is %q", schema.Namespace,
			postgres.ConfigNamespace)
	}

	declared := make(map[string]bool, len(schema.Items))
	for key := range schema.Items {
		declared[key] = true
	}
	for _, key := range sharedItems {
		if !declared[key] {
			t.Errorf("the shared item %q is missing from this implementation's declaration, and every "+
				"implementation accepts the same ones", key)
		}
	}
	if !declared[db.MigrationLockTimeoutKey] {
		t.Errorf("the item %q is missing, and this implementation is the one whose dialect owes a "+
			"cross-process mutex: the item is declared exactly when a mutex was supplied",
			db.MigrationLockTimeoutKey)
	}
	if len(declared) != len(sharedItems)+1 {
		t.Errorf("the section declares %d items and the design gives it %d",
			len(declared), len(sharedItems)+1)
	}

	if module.Prepare == nil {
		t.Error("the module has no Prepare, so it never states whether it runs: a host with no locator " +
			"configured would fail the startup instead of running without a database")
	}
	if module.New == nil {
		t.Error("the module has no New, so no connection is ever established")
	}
	if module.Migrate == nil {
		t.Error("the module has no Migrate, so the declared migration sets are never applied")
	}
	if module.Close == nil {
		t.Error("the module has no Close, so the connection pool is never released")
	}
	if module.Stop != nil {
		t.Error("the module carries a Stop callback, and this module has none: draining is each " +
			"module's own responsibility for its in-flight work, and the pool is released in Close")
	}
}

// TestTheProcessRegistrationCarriesTheModule pins the init registration.
func TestTheProcessRegistrationCarriesTheModule(t *testing.T) {
	registered, ok := core.ProcessRegistry.Lookup(postgres.ModuleName)
	if !ok {
		t.Fatalf("importing this package did not register %q with the process registry, so a host that "+
			"imports it runs without a database", postgres.ModuleName)
	}
	built := postgres.Module()
	if !reflect.DeepEqual(registered.Provides, built.Provides) {
		t.Errorf("the registered descriptor delivers %v, and Module builds %v: the registration is the "+
			"descriptor hosts actually run on", registered.Provides, built.Provides)
	}
	dbsuite.RequireOptionalLogging(t, "the module this subpackage registers", registered)
	registeredSchema, builtSchema := schemaOf(t, registered), schemaOf(t, built)
	if !reflect.DeepEqual(registeredSchema, builtSchema) {
		t.Errorf("the registration mounts its input items on %q and Module builds the declaration for "+
			"%q, so hosts mount theirs through a declaration no case here has looked at",
			registeredSchema.Namespace, builtSchema.Namespace)
	}
}

func schemaOf(t *testing.T, module core.Module) config.Schema {
	t.Helper()
	if len(module.Resources) != 1 {
		t.Fatalf("the module carries %d resources, and an implementation carries its input item "+
			"declaration and nothing else", len(module.Resources))
	}
	schema, ok := module.Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module's resource is %T, and an implementation hands over a config.Schema",
			module.Resources[0])
	}
	return schema
}

const dependantName = "postgres_test.dependant"

const dependantFile = "0001_widgets.sql"

func widgetsMigrations() fstest.MapFS {
	return fstest.MapFS{
		"postgres/" + dependantFile: &fstest.MapFile{Data: []byte(
			"CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\n" +
				"CREATE INDEX widgets_name_idx ON widgets (name);\n")},
	}
}

type dependant struct {
	delivered chan db.Database
	migrated  chan struct{}
}

func (d *dependant) module() core.Module {
	return core.Module{
		Name:      dependantName,
		Requires:  []core.Requirement{{Token: (*db.Database)(nil)}},
		Resources: []any{db.Migrations{FS: widgetsMigrations()}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			instance, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			d.delivered <- instance
			return struct{}{}, nil
		},
		Init: func(context.Context, *core.Registry, any) error {
			close(d.migrated)
			return nil
		},
	}
}

func runAssemblySupplying(t *testing.T, parent context.Context, section map[string]any,
	before func(*core.Registry),
) (db.Database, error) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"db": map[string]any{"postgres": section},
	})
	if err != nil {
		t.Fatalf("building the primary configuration source: %v", err)
	}
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, body, 0o600); err != nil {
		t.Fatalf("writing the primary configuration source: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDPOSTGRESTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(postgres.Module())
	d := &dependant{delivered: make(chan db.Database, 1), migrated: make(chan struct{})}
	reg.Register(d.module())
	if before != nil {
		before(reg)
	}

	// The command line is the process's and a test binary carries arguments
	// of its own, so they are taken away while the tree reads them. The cases
	// here therefore do not run in parallel.
	previous := os.Args
	os.Args = []string{"postgres.test"}

	app := config.NewApp(reg)
	app.Root().IncludeAll()
	app.Root().Do(core.StageServe, nil)

	ctx, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx) }()

	var instance db.Database
	select {
	case err := <-done:
		os.Args = previous
		cancel()
		return nil, err
	case instance = <-d.delivered:
		os.Args = previous
	}
	select {
	case err := <-done:
		cancel()
		return nil, err
	case <-d.migrated:
	}
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the host shut down with an error: %v", err)
		}
	})
	return instance, nil
}

type hostDatabase struct{}

func (hostDatabase) DB() *gorm.DB                       { return nil }
func (hostDatabase) Dialect() db.Dialect                { return postgres.Dialect }
func (hostDatabase) BlindIndex(plaintext string) string { return plaintext }

func (hostDatabase) Open(context.Context, string) (*gorm.DB, error) {
	return nil, errors.New("the host's own implementation opens no further connection")
}

var _ db.Database = hostDatabase{}

// TestAForeignInstanceSuppliedForThisModuleFailsTheStartup checks that an instance of db.Database
// the host wrote fails the startup.
func TestAForeignInstanceSuppliedForThisModuleFailsTheStartup(t *testing.T) {
	_, err := runAssemblySupplying(t, context.Background(), map[string]any{}, func(reg *core.Registry) {
		reg.Supply(hostDatabase{}, postgres.ModuleName)
	})
	if !errors.Is(err, db.ErrForeignInstance) {
		t.Fatalf("a startup over an implementation of db.Database the host wrote ended with %v, "+
			"want db.ErrForeignInstance", err)
	}
}
