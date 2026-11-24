package postgres_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/postgres"
)

// TestConfigNamespaceIsThisImplementationsOwn pins the namespace apart from every other
// implementation's.
func TestConfigNamespaceIsThisImplementationsOwn(t *testing.T) {
	if postgres.ConfigNamespace != "db.postgres" {
		t.Errorf("the config namespace is %q, and the design names it db.postgres", postgres.ConfigNamespace)
	}
	if postgres.ConfigNamespace != postgres.ModuleName {
		t.Errorf("the config namespace %q and the module name %q differ, and the design has a module's "+
			"name and its configuration section be the same", postgres.ConfigNamespace, postgres.ModuleName)
	}
	prefix, own, found := strings.Cut(postgres.ConfigNamespace, ".")
	if prefix != "db" || !found || own == "" {
		t.Errorf("the config namespace %q is not a section of its own under db, so a second "+
			"implementation's items would land on the same paths", postgres.ConfigNamespace)
	}
}

// TestDialectIsPostgres pins the dialect this subpackage binds.
func TestDialectIsPostgres(t *testing.T) {
	if postgres.Dialect != db.Postgres {
		t.Errorf("this subpackage binds the dialect %q, and the capability names it %q",
			postgres.Dialect, db.Postgres)
	}
	if strings.ContainsAny(string(postgres.Dialect), "/\\.") {
		t.Errorf("the dialect %q is not a single path element, so it cannot name a migration "+
			"subdirectory", postgres.Dialect)
	}
}

// TestMigrationLockTimeoutIsDeclaredWithADefault holds this implementation to the input item its
// dialect owes.
func TestMigrationLockTimeoutIsDeclaredWithADefault(t *testing.T) {
	if postgres.MigrationLockTimeoutKey != "migration-lock-timeout" {
		t.Errorf("the lock timeout item is keyed %q, and the design names it migration-lock-timeout",
			postgres.MigrationLockTimeoutKey)
	}
	if postgres.DefaultMigrationLockTimeout.String() != "5m0s" {
		t.Errorf("the lock timeout defaults to %s, and the design gives 5 minutes",
			postgres.DefaultMigrationLockTimeout)
	}
}

// TestPostgresDoesNotImportTheSQLiteDriver is the manual gate on the boundary between the two
// implementations.
func TestPostgresDoesNotImportTheSQLiteDriver(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")

	var sawOwnDriver bool
	for _, line := range deps {
		pkg := strings.TrimSpace(line)
		switch {
		case pkg == "gorm.io/driver/postgres":
			sawOwnDriver = true
		case strings.Contains(pkg, "sqlite"):
			t.Errorf("the PostgreSQL implementation depends on %s, so a host deploying on PostgreSQL "+
				"carries the SQLite driver too", pkg)
		case strings.HasPrefix(pkg, "gorm.io/driver/") && pkg != "gorm.io/driver/postgres":
			t.Errorf("the PostgreSQL implementation depends on %s, which is another engine's driver", pkg)
		}
	}
	if !sawOwnDriver {
		t.Errorf("the dependency listing does not contain this engine's own driver, so it did not load "+
			"the package and the assertions above checked nothing (%d lines)", len(deps))
	}
}
