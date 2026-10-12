package policy

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The migration files are the schema the module ships, and these cases run the
// files themselves rather than a copy written out in the test. What the
// deciding process relies on is asserted: three tables, the tenant column
// leading each key, and a tenant column that may not be empty.

// The SQLite migration builds the three tenant-keyed tables.
func TestTheSqliteMigrationBuildsTheTenantKeyedTables(t *testing.T) {
	handle := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil).DB()
	applyMigration(t, handle, "sqlite/0001_authz_policy.sql")

	for _, table := range []string{"authz_roles", "authz_grants", "authz_bindings"} {
		if !handle.Migrator().HasTable(table) {
			t.Fatalf("the migration built no %s table", table)
		}
		assertTenantLeadsTheKey(t, handle, table)
	}
	assertIndexesExist(t, handle, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?",
		"authz_grants_by_object", "authz_bindings_by_role")
	assertColumnType(t, handle, "authz_roles", "created_at", "DATETIME")
	assertColumnType(t, handle, "authz_grants", "created_at", "DATETIME")
	assertColumnType(t, handle, "authz_bindings", "granted_at", "DATETIME")
	assertTenantKeyedIsolation(t, handle)
	assertEmptyTenantRefused(t, handle)
}

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

// assertTenantLeadsTheKey reads the table's primary-key order and holds it to
// the tenant column leading. The migration is the only place the key is stated,
// so this is what keeps a key that named a non-tenant column first from
// reaching the tables the deciding process reads.
func assertTenantLeadsTheKey(t *testing.T, handle *gorm.DB, table string) {
	t.Helper()
	var columns []struct {
		Name string
		Pk   int
	}
	if err := handle.Raw("PRAGMA table_info(" + table + ")").Scan(&columns).Error; err != nil {
		t.Fatalf("reading the columns of %s: %v", table, err)
	}
	var lead string
	for _, column := range columns {
		if column.Pk == 1 {
			lead = column.Name
		}
	}
	if lead != "tenant_id" {
		t.Errorf("%s leads its key with %q, want tenant_id", table, lead)
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

// assertIndexesExist holds that the migration built each named index, which the
// design's 表与迁移 lists: the deciding queries read by (tenant, resource,
// action) and by (tenant, role). countSQL is a dialect's one-name query; each
// name is put to it.
func assertIndexesExist(t *testing.T, handle *gorm.DB, countSQL string, names ...string) {
	t.Helper()
	for _, name := range names {
		var count int64
		if err := handle.Raw(countSQL, name).Row().Scan(&count); err != nil {
			t.Fatalf("reading the index %s: %v", name, err)
		}
		if count == 0 {
			t.Errorf("the migration built no index %s", name)
		}
	}
}

// assertColumnType holds a SQLite column to its declared type, which the design
// fixes per dialect: SQLite's DATETIME.
func assertColumnType(t *testing.T, handle *gorm.DB, table, column, want string) {
	t.Helper()
	var columns []struct {
		Name string
		Type string
	}
	if err := handle.Raw("PRAGMA table_info(" + table + ")").Scan(&columns).Error; err != nil {
		t.Fatalf("reading the columns of %s: %v", table, err)
	}
	for _, c := range columns {
		if c.Name == column {
			if !strings.EqualFold(c.Type, want) {
				t.Errorf("%s.%s has type %q, want %q", table, column, c.Type, want)
			}
			return
		}
	}
	t.Errorf("%s has no column %s", table, column)
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

// assertTenantKeyedIsolation writes rows that are identical but for the tenant
// into each table and holds that they stay separate.
//
// With the tenant column leading the key the role, the account and a role's
// (resource, action) are each not unique on their own, so a second tenant's
// write lands as its own row. A key that named only the non-tenant column would
// make that write collide with the first tenant's row and rewrite its tenant,
// with no other tenant named in the statement to refuse.
func assertTenantKeyedIsolation(t *testing.T, handle *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{
			"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, created_at) VALUES (?,?,?,?,?)",
			[]any{"acme", "roleA", false, "acme's", now},
		},
		{
			"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, created_at) VALUES (?,?,?,?,?)",
			[]any{"globex", "roleA", false, "globex's", now},
		},
		{
			"INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES (?,?,?,?,?)",
			[]any{"acme", "roleA", "account", "read", now},
		},
		{
			"INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES (?,?,?,?,?)",
			[]any{"globex", "roleA", "account", "read", now},
		},
		{
			"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
			[]any{"acme", "acc1", "roleA", now},
		},
		{
			"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
			[]any{"globex", "acc1", "roleA", now},
		},
	} {
		if err := handle.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatalf("writing a second tenant's row: %v", err)
		}
	}

	// A write aimed at one tenant's row leaves the other tenant's untouched.
	if err := handle.Exec("UPDATE authz_roles SET description_key = ? WHERE tenant_id = ? AND role = ?",
		"updated", "acme", "roleA").Error; err != nil {
		t.Fatalf("updating acme's role: %v", err)
	}
	if err := handle.Exec("UPDATE authz_grants SET action = ? WHERE tenant_id = ? AND role = ? AND resource = ?",
		"write", "acme", "roleA", "account").Error; err != nil {
		t.Fatalf("updating acme's grant: %v", err)
	}

	for _, c := range []struct {
		sql  string
		args []any
		want any
	}{
		{"SELECT COUNT(*) FROM authz_roles WHERE role = ?", []any{"roleA"}, int64(2)},
		{"SELECT COUNT(*) FROM authz_grants WHERE role = ?", []any{"roleA"}, int64(2)},
		{"SELECT COUNT(*) FROM authz_bindings WHERE account_id = ?", []any{"acc1"}, int64(2)},
		{"SELECT description_key FROM authz_roles WHERE tenant_id = ? AND role = ?", []any{"globex", "roleA"}, "globex's"},
		{"SELECT action FROM authz_grants WHERE tenant_id = ? AND role = ? AND resource = ?", []any{"globex", "roleA", "account"}, "read"},
	} {
		var got any
		row := handle.Raw(c.sql, c.args...).Row()
		if err := row.Scan(&got); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
		if got != c.want {
			t.Errorf("%s gave %v, want %v", c.sql, got, c.want)
		}
	}
}

// assertEmptyTenantRefused holds each table to its non-empty tenant column: a
// row belonging to no tenant would reach no tenant's decision while the
// cross-tenant mark let it through.
func assertEmptyTenantRefused(t *testing.T, handle *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{
			"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, created_at) VALUES ('',?,?,?,?)",
			[]any{"roleA", false, "k", now},
		},
		{
			"INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES ('',?,?,?,?)",
			[]any{"roleA", "account", "read", now},
		},
		{
			"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES ('',?,?,?)",
			[]any{"acc1", "roleA", now},
		},
	} {
		if err := handle.Exec(statement.sql, statement.args...).Error; err == nil {
			t.Errorf("a row with no tenant was accepted: %s", statement.sql)
		}
	}
}
