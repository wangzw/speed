package catalog

import (
	"database/sql"
	"io/fs"
	"strings"
	"testing"
)

// TestTheOwnerAndDeletionMigrationDeclaresItsColumnsForBothDialects checks the
// fourth migration of each dialect declares the owner columns and their index.
func TestTheOwnerAndDeletionMigrationDeclaresItsColumnsForBothDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			handle := openHandle(t)
			for _, name := range []string{"0001_tenants.sql", "0002_tenants_disabled_by.sql", "0003_tenants_profile.sql"} {
				ddl, err := fs.ReadFile(migrationsFS, "sqlite/"+name)
				if err != nil {
					t.Fatalf("reading sqlite/%s: %v", name, err)
				}
				if err := handle.Exec(string(ddl)).Error; err != nil {
					t.Fatalf("applying sqlite/%s: %v", name, err)
				}
			}
			ddl, err := fs.ReadFile(migrationsFS, dialect+"/0004_tenants_owner_deleted.sql")
			if err != nil {
				t.Fatalf("reading the %s owner migration: %v", dialect, err)
			}
			stmt := string(ddl)
			if dialect == "postgres" {
				stmt = strings.ReplaceAll(stmt, " IF NOT EXISTS", "")
			}
			if err := handle.Exec(stmt).Error; err != nil {
				t.Fatalf("applying the %s owner migration: %v", dialect, err)
			}

			var columns []struct {
				Name    string
				NotNull int            `gorm:"column:notnull"`
				Default sql.NullString `gorm:"column:dflt_value"`
			}
			if err := handle.Raw("PRAGMA table_info(tenants)").Scan(&columns).Error; err != nil {
				t.Fatalf("reading the columns of tenants: %v", err)
			}
			held := make(map[string]struct {
				notNull bool
				def     string
			}, len(columns))
			for _, column := range columns {
				held[column.Name] = struct {
					notNull bool
					def     string
				}{notNull: column.NotNull != 0, def: column.Default.String}
			}

			owner, ok := held["owner_account"]
			if !ok {
				t.Fatalf("the %s migration declares no owner_account column", dialect)
			}
			if !owner.notNull || owner.def != "''" {
				t.Errorf("tenants.owner_account is NOT NULL=%v default %q, want NOT NULL default ''",
					owner.notNull, owner.def)
			}
			deleted, ok := held["deleted_at"]
			if !ok {
				t.Fatalf("the %s migration declares no deleted_at column", dialect)
			}
			if deleted.notNull {
				t.Errorf("tenants.deleted_at is NOT NULL, want nullable")
			}

			var indexed int64
			if err := handle.Raw(
				"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'tenants' " +
					"AND sql LIKE '%owner_account%'").Scan(&indexed).Error; err != nil {
				t.Fatalf("reading the indexes on tenants: %v", err)
			}
			if indexed == 0 {
				t.Errorf("the %s migration declares no index on owner_account", dialect)
			}
		})
	}
}
