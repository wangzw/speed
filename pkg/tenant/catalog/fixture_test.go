package catalog

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/tenant"
)

// The helpers the statement-shape cases share. Each case drives a shape GORM
// accepts besides the plain Find, Create, Update and Delete that plugin_test.go
// covers, and names the answer the filter owes it: the rows of the context's
// tenant, a refusal, or GORM's own answer where the filter has nothing to add.

// crossContext is a context that crosses tenants on purpose.
func crossContext() context.Context { return tenant.CrossTenant(context.Background()) }

// tenantsOf reads every row's tenant past the filter, keyed by row identifier.
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

// requireRowTenant asserts that the one row with this identifier belongs to
// the tenant named.
func requireRowTenant(t *testing.T, handle *gorm.DB, id, want string) {
	t.Helper()
	got := tenantsOf(t, handle)[id]
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the rows identified by %s belong to %q, want just %q", id, got, want)
	}
}

// requireMismatch asserts that a write was refused as naming another tenant.
func requireMismatch(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, tenant.ErrExplicitTenantMismatch) {
		t.Fatalf("the write gave %v, want ErrExplicitTenantMismatch", err)
	}
}
