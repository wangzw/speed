//go:build integration

package policy

import (
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The PostgreSQL migration builds the same tables and holds the same keying,
// so a suite that ran on SQLite alone would not leave the engine the module is
// deployed against untested.
func TestThePostgresMigrationBuildsTheTenantKeyedTables(t *testing.T) {
	handle := dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
	applyMigration(t, handle, "postgres/0001_authz_policy.sql")

	for _, table := range []string{"authz_roles", "authz_grants", "authz_bindings"} {
		if !handle.Migrator().HasTable(table) {
			t.Fatalf("the migration built no %s table", table)
		}
		assertTenantLeadsTheKeyPostgres(t, handle, table)
	}
	assertIndexesExist(t, handle, "SELECT COUNT(*) FROM pg_indexes WHERE indexname = ?",
		"authz_grants_by_object", "authz_bindings_by_role")
	assertColumnTypePostgres(t, handle, "authz_roles", "created_at", "timestamp with time zone")
	assertColumnTypePostgres(t, handle, "authz_grants", "created_at", "timestamp with time zone")
	assertColumnTypePostgres(t, handle, "authz_bindings", "granted_at", "timestamp with time zone")
	assertTenantKeyedIsolation(t, handle)
	assertEmptyTenantRefused(t, handle)
}

// The second PostgreSQL migration is the same data migration as the SQLite one,
// so the deployment dialect is held to it as well rather than only the SQLite
// copy the ordinary suite runs.
func TestThePostgresMigrationWithdrawsThePlatformAdministratorOutsideTheSystemDomain(t *testing.T) {
	handle := dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
	applyMigration(t, handle, "postgres/0001_authz_policy.sql")
	seedTenantAdministrationRows(t, handle, "system", "acme")

	applyMigration(t, handle, "postgres/0002_tenant_administration.sql")
	assertPlatformAdministratorWithdrawn(t, handle)
	applyMigration(t, handle, "postgres/0002_tenant_administration.sql")
	assertPlatformAdministratorWithdrawn(t, handle)
}

// The third PostgreSQL migration is the same schema change as the SQLite one:
// the role's free-text description, defaulting to the empty string, and the
// (tenant, role, account) index the role-usage page reads through.
func TestThePostgresRoleDescriptionMigrationAddsTheDescriptionAndRoleBindingIndex(t *testing.T) {
	handle := dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
	applyMigration(t, handle, "postgres/0001_authz_policy.sql")
	applyMigration(t, handle, "postgres/0003_role_description.sql")

	assertColumnTypePostgres(t, handle, "authz_roles", "description", "text")
	var nullable, defaultValue string
	if err := handle.Raw(
		`SELECT is_nullable, COALESCE(column_default, '') FROM information_schema.columns
		 WHERE table_name = ? AND column_name = ?`, "authz_roles", "description").
		Row().Scan(&nullable, &defaultValue); err != nil {
		t.Fatalf("reading the description column's default: %v", err)
	}
	if nullable != "NO" {
		t.Errorf("authz_roles.description is nullable, want NOT NULL")
	}
	if !strings.Contains(defaultValue, "''") {
		t.Errorf("authz_roles.description defaults to %q, want the empty string", defaultValue)
	}

	assertIndexesExist(t, handle, "SELECT COUNT(*) FROM pg_indexes WHERE indexname = ?",
		"authz_bindings_by_role_account")
	var definition string
	if err := handle.Raw("SELECT indexdef FROM pg_indexes WHERE indexname = ?",
		"authz_bindings_by_role_account").Row().Scan(&definition); err != nil {
		t.Fatalf("reading the index definition: %v", err)
	}
	if !strings.Contains(definition, "(tenant_id, role, account_id)") {
		t.Errorf("authz_bindings_by_role_account covers %q, want (tenant_id, role, account_id)", definition)
	}
}

// assertTenantLeadsTheKeyPostgres is assertTenantLeadsTheKey for PostgreSQL,
// whose catalog is read through information_schema rather than a PRAGMA. The
// keying is the same claim on both dialects, so each holds it for itself.
func assertTenantLeadsTheKeyPostgres(t *testing.T, handle *gorm.DB, table string) {
	t.Helper()
	var lead string
	err := handle.Raw(`
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_name = ?
		ORDER BY kcu.ordinal_position
		LIMIT 1`, table).Row().Scan(&lead)
	if err != nil {
		t.Fatalf("reading the key of %s: %v", table, err)
	}
	if lead != "tenant_id" {
		t.Errorf("%s leads its key with %q, want tenant_id", table, lead)
	}
}

// assertColumnTypePostgres is assertColumnType for PostgreSQL, whose declared
// timestamp type is TIMESTAMPTZ.
func assertColumnTypePostgres(t *testing.T, handle *gorm.DB, table, column, want string) {
	t.Helper()
	var got string
	err := handle.Raw(
		`SELECT data_type FROM information_schema.columns WHERE table_name = ? AND column_name = ?`,
		table, column).Row().Scan(&got)
	if err != nil {
		t.Fatalf("reading the type of %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Errorf("%s.%s has type %q, want %q", table, column, got, want)
	}
}
