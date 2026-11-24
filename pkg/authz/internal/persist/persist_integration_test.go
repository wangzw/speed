//go:build integration

package persist

import (
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

var postgresDDL = []string{
	`CREATE TABLE authz_roles (
		tenant_id       TEXT NOT NULL CHECK (tenant_id <> ''),
		role            TEXT NOT NULL,
		builtin         BOOLEAN NOT NULL,
		description_key TEXT NOT NULL,
		created_at      TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (tenant_id, role))`,
	`CREATE TABLE authz_grants (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		role       TEXT NOT NULL,
		resource   TEXT NOT NULL,
		action     TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (tenant_id, role, resource, action))`,
	`CREATE INDEX authz_grants_by_object ON authz_grants (tenant_id, resource, action)`,
	`CREATE TABLE authz_bindings (
		tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
		account_id TEXT NOT NULL,
		role       TEXT NOT NULL,
		granted_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (tenant_id, account_id, role))`,
	`CREATE INDEX authz_bindings_by_role ON authz_bindings (tenant_id, role)`,
}

func init() {
	allDialects = append(allDialects, func() dialect {
		return dialect{
			name: "postgres",
			open: func(t *testing.T) *gorm.DB {
				t.Helper()
				handle, err := gorm.Open(postgres.Open(pgtest.Acquire(t)), &gorm.Config{})
				if err != nil {
					t.Fatalf("opening the PostgreSQL handle: %v", err)
				}
				closePool(t, handle)
				return handle
			},
			ddl: postgresDDL,
		}
	})
}
