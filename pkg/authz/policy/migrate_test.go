package policy

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/internal/dbtest"
)

// TestTheSqliteMigrationBuildsTheTenantKeyedTables checks that the SQLite migration builds the tenant keyed tables.
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

// TestTheSqliteMigrationWithdrawsThePlatformAdministratorOutsideTheSystemDomain checks that the SQLite migration withdraws the platform administrator outside the system domain.
func TestTheSqliteMigrationWithdrawsThePlatformAdministratorOutsideTheSystemDomain(t *testing.T) {
	handle := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil).DB()
	applyMigration(t, handle, "sqlite/0001_authz_policy.sql")
	seedTenantAdministrationRows(t, handle, "system", "acme")

	applyMigration(t, handle, "sqlite/0002_tenant_administration.sql")
	assertPlatformAdministratorWithdrawn(t, handle)
	applyMigration(t, handle, "sqlite/0002_tenant_administration.sql")
	assertPlatformAdministratorWithdrawn(t, handle)
}

// TestTheSqliteRoleDescriptionMigrationAddsTheDescriptionAndRoleBindingIndex checks that the SQLite role description migration adds the description and role binding index.
func TestTheSqliteRoleDescriptionMigrationAddsTheDescriptionAndRoleBindingIndex(t *testing.T) {
	handle := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil).DB()
	applyMigration(t, handle, "sqlite/0001_authz_policy.sql")
	applyMigration(t, handle, "sqlite/0003_role_description.sql")

	assertColumnType(t, handle, "authz_roles", "description", "TEXT")

	now := time.Now().UTC()
	if err := handle.Exec(
		"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, created_at) VALUES (?,?,?,?,?)",
		"acme", "roleA", false, "k", now).Error; err != nil {
		t.Fatalf("writing a role that names no description: %v", err)
	}
	var description string
	if err := handle.Raw(
		"SELECT description FROM authz_roles WHERE tenant_id = ? AND role = ?", "acme", "roleA").
		Row().Scan(&description); err != nil {
		t.Fatalf("reading the description: %v", err)
	}
	if description != "" {
		t.Errorf("a role written with no description reads back %q, want the empty default", description)
	}
	if err := handle.Exec(
		"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, description, created_at) VALUES (?,?,?,?,?,?)",
		"acme", "roleB", false, "k", nil, now).Error; err == nil {
		t.Error("the description column accepted a null")
	}

	assertIndexesExist(t, handle, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?",
		"authz_bindings_by_role_account")
	var columns []struct{ Name string }
	if err := handle.Raw("PRAGMA index_info(authz_bindings_by_role_account)").Scan(&columns).Error; err != nil {
		t.Fatalf("reading the index columns: %v", err)
	}
	got := make([]string, len(columns))
	for i, column := range columns {
		got[i] = column.Name
	}
	if want := []string{"tenant_id", "role", "account_id"}; !slices.Equal(got, want) {
		t.Errorf("authz_bindings_by_role_account covers %v, want %v", got, want)
	}
}

func seedTenantAdministrationRows(t *testing.T, handle *gorm.DB, platform string, tenants ...string) {
	t.Helper()
	now := time.Now().UTC()
	domains := append([]string{platform}, tenants...)
	for _, domain := range domains {
		for _, role := range []string{"platform-admin", "tenant-admin"} {
			for _, statement := range []struct {
				sql  string
				args []any
			}{
				{
					"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, created_at) VALUES (?,?,?,?,?)",
					[]any{domain, role, false, "k", now},
				},
				{
					"INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES (?,?,?,?,?)",
					[]any{domain, role, "tenant", "read", now},
				},
				{
					"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
					[]any{domain, "acct-" + domain, role, now},
				},
			} {
				if err := handle.Exec(statement.sql, statement.args...).Error; err != nil {
					t.Fatalf("seeding %s/%s: %v", domain, role, err)
				}
			}
		}
	}
}

func assertPlatformAdministratorWithdrawn(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, c := range []struct {
		sql  string
		want int64
	}{
		{"SELECT COUNT(*) FROM authz_roles WHERE role = 'platform-admin' AND tenant_id <> 'system'", 0},
		{"SELECT COUNT(*) FROM authz_grants WHERE role = 'platform-admin' AND tenant_id <> 'system'", 0},
		{"SELECT COUNT(*) FROM authz_bindings WHERE role = 'platform-admin' AND tenant_id <> 'system'", 0},
		{"SELECT COUNT(*) FROM authz_roles WHERE role = 'platform-admin' AND tenant_id = 'system'", 1},
		{"SELECT COUNT(*) FROM authz_grants WHERE role = 'platform-admin' AND tenant_id = 'system'", 1},
		{"SELECT COUNT(*) FROM authz_bindings WHERE role = 'platform-admin' AND tenant_id = 'system'", 1},
		{"SELECT COUNT(*) FROM authz_roles WHERE role = 'tenant-admin'", 2},
		{"SELECT COUNT(*) FROM authz_grants WHERE role = 'tenant-admin'", 2},
		{"SELECT COUNT(*) FROM authz_bindings WHERE role = 'tenant-admin'", 2},
	} {
		var got int64
		if err := handle.Raw(c.sql).Row().Scan(&got); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
		if got != c.want {
			t.Errorf("%s = %d, want %d", c.sql, got, c.want)
		}
	}
}

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
