package sqlite_test

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
	"github.com/vislake/speed/pkg/db/sqlite"
)

const twoStatements = `CREATE TABLE widgets (id INTEGER NOT NULL, PRIMARY KEY (id));
CREATE INDEX widgets_by_id ON widgets (id)`

// TestSQLiteRollsBackDDL holds this engine to the first admission condition of an
// implementation subpackage, through the dialector this subpackage exports.
func TestSQLiteRollsBackDDL(t *testing.T) {
	handle := open(t, filepath.Join(t.TempDir(), "rolled-back.db"))

	rollback := errors.New("rolling this transaction back on purpose")
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE TABLE rolled_back (id INTEGER PRIMARY KEY)`).Error; err != nil {
			return err
		}
		if !tx.Migrator().HasTable("rolled_back") {
			t.Error("the table is not there inside the transaction that created it")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("the transaction reported %v, and the rollback this case turns on did not happen", err)
	}
	if handle.Migrator().HasTable("rolled_back") {
		t.Error("the table created inside the rolled-back transaction is still there, so this engine " +
			"keeps DDL outside the transaction and a migration's execution and its record are not atomic")
	}
}

// TestSQLiteRunsAMigrationFileWithSeveralStatements holds this engine to the second admission
// condition, through the dialector this subpackage exports.
func TestSQLiteRunsAMigrationFileWithSeveralStatements(t *testing.T) {
	handle := open(t, filepath.Join(t.TempDir(), "several-statements.db"))

	if err := handle.Exec(twoStatements).Error; err != nil {
		t.Fatalf("this engine would not run a text of two statements, so a migration set holding one "+
			"stops working on it: %v", err)
	}
	if !handle.Migrator().HasTable("widgets") {
		t.Error("the first statement of the text did not take effect")
	}
	if !handle.Migrator().HasIndex("widgets", "widgets_by_id") {
		t.Error("only the first statement of the text was executed and the rest was dropped without a word")
	}
}

// TestSQLiteDeclaresNoMigrationLockTimeout pins that this implementation offers no cross-process
// mutex.
func TestSQLiteDeclaresNoMigrationLockTimeout(t *testing.T) {
	schema := firstSchema(t)
	if _, declared := schema.Items[db.MigrationLockTimeoutKey]; declared {
		t.Errorf("this implementation declares %s, and it supplies no mutex to wait for",
			db.MigrationLockTimeoutKey)
	}

	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":"x","%s":"5m"}}}`, db.MigrationLockTimeoutKey)
	_, err := load(t, body, sqlite.Module())
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("setting %s on this implementation gave %v, want config.ErrUnknownKey",
			db.MigrationLockTimeoutKey, err)
	}
}

// TestSQLiteModuleShape pins what this subpackage registers: the name, the namespace its input
// items hang under, the capability it delivers exclusively, the capability it takes up optionally.
func TestSQLiteModuleShape(t *testing.T) {
	module := sqlite.Module()

	if module.Name != "db.sqlite" {
		t.Errorf("the module is named %q, and the design names it db.sqlite", module.Name)
	}
	if sqlite.ConfigNamespace != "db.sqlite" {
		t.Errorf("the config namespace is %q, and the design names it db.sqlite", sqlite.ConfigNamespace)
	}
	if sqlite.ConfigNamespace != sqlite.ModuleName {
		t.Errorf("the config namespace %q and the module name %q differ, and a module's name and its "+
			"configuration section are the same thing", sqlite.ConfigNamespace, sqlite.ModuleName)
	}
	if sqlite.Dialect != db.SQLite {
		t.Errorf("this subpackage binds the dialect %q, and the capability names it %q",
			sqlite.Dialect, db.SQLite)
	}
	if strings.ContainsAny(string(sqlite.Dialect), `/\`) {
		t.Errorf("the dialect %q is not a single path element, so it cannot name a migration "+
			"subdirectory", sqlite.Dialect)
	}

	if len(module.Provides) != 1 {
		t.Fatalf("this module declares %d provisions, and it delivers the database capability alone",
			len(module.Provides))
	}
	provision := module.Provides[0]
	if _, delivers := provision.Token.(*db.Database); !delivers {
		t.Errorf("this module delivers %T, want the database capability", provision.Token)
	}
	if !provision.Exclusive {
		t.Error("this module does not claim the database capability exclusively, so a host that " +
			"configured two engines would silently get one of them")
	}
	dbsuite.RequireOptionalLogging(t, "the module this subpackage builds", module)

	stages := []struct {
		name string
		set  bool
	}{
		{"Prepare", module.Prepare != nil},
		{"New", module.New != nil},
		{"Migrate", module.Migrate != nil},
		{"Close", module.Close != nil},
	}
	for _, stage := range stages {
		if !stage.set {
			t.Errorf("this module has no %s stage, so the work that stage carries never runs", stage.name)
		}
	}

	registered, ok := core.ProcessRegistry.Lookup(module.Name)
	if !ok {
		t.Fatalf("importing this package did not register %s in the process registry, so a host "+
			"running on SQLite would find no module to configure", module.Name)
	}
	// The registration has to be the descriptor this subpackage builds, not a
	// name under which nothing is delivered: a stub satisfies the lookup
	// above and leaves every dependant without a handle.
	if len(registered.Provides) != 1 || !registered.Provides[0].Exclusive || registered.Migrate == nil {
		t.Errorf("the process registry holds a descriptor under %s that is not the one this "+
			"subpackage builds: it delivers nothing, or does not claim the capability exclusively, "+
			"or applies no migrations", module.Name)
	}
	dbsuite.RequireOptionalLogging(t, "the module this subpackage registers", registered)
}

// TestSQLiteDoesNotImportThePostgresDriver is the manual gate on the boundary between the two
// implementations.
func TestSQLiteDoesNotImportThePostgresDriver(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")

	var sawOwnDriver bool
	for _, line := range deps {
		pkg := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(pkg, "github.com/glebarez/"):
			sawOwnDriver = true
		case strings.Contains(pkg, "postgres"):
			t.Errorf("the SQLite implementation depends on %s, so a host deploying on SQLite carries "+
				"the PostgreSQL driver too", pkg)
		case strings.HasPrefix(pkg, "gorm.io/driver/"):
			t.Errorf("the SQLite implementation depends on %s, which is a GORM driver this "+
				"subpackage does not bind", pkg)
		}
	}
	if !sawOwnDriver {
		t.Errorf("the dependency listing does not contain this engine's own driver, so it did not load "+
			"the package and the assertions above checked nothing (%d lines)", len(deps))
	}
}

func firstSchema(t *testing.T) config.Schema {
	t.Helper()
	module := sqlite.Module()
	if len(module.Resources) != 1 {
		t.Fatalf("the module carries %d resources, and it declares its input items in one",
			len(module.Resources))
	}
	schema, ok := module.Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module's resource is %T, want a config.Schema", module.Resources[0])
	}
	return schema
}

func open(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(sqlite.Dialector(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening the SQLite fixture at %s: %v", dsn, err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the fixture's connection pool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("closing the fixture: %v", err)
		}
	})
	return handle
}
