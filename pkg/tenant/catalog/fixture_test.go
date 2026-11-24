package catalog

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

type handleDialect struct {
	name string
	open func(*testing.T) *gorm.DB
}

// PostgreSQL is appended to shapeDialects by the tagged file.
var shapeDialects = []handleDialect{
	{name: "sqlite", open: openShapesOnSQLite},
}

// PostgreSQL is appended to filteredDialects by the tagged file.
var filteredDialects = []handleDialect{
	{name: "sqlite", open: open},
}

func crossContext() context.Context { return tenant.CrossTenant(context.Background()) }

func tenantsOf(t *testing.T, handle *gorm.DB) map[string][]string {
	t.Helper()
	var rows []struct {
		TenantID string
		ID       string
	}
	if err := handle.WithContext(crossContext()).
		Raw("SELECT tenant_id, id FROM orders ORDER BY id, tenant_id").
		Scan(&rows).Error; err != nil {
		t.Fatalf("reading the table back: %v", err)
	}
	out := make(map[string][]string, len(rows))
	for _, r := range rows {
		out[r.ID] = append(out[r.ID], r.TenantID)
	}
	return out
}

func requireRowTenant(t *testing.T, handle *gorm.DB, id, want string) {
	t.Helper()
	got := tenantsOf(t, handle)[id]
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the rows identified by %s belong to %q, want just %q", id, got, want)
	}
}

func requireMismatch(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
		t.Fatalf("the write gave %v, want ErrExplicitTenantMismatch", err)
	}
}
