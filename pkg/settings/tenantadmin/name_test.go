package tenantadmin

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestTheAuditDetailNamesTheTenantName checks that the mandatory row carries the tenant's name, not its internal identifier.
func TestTheAuditDetailNamesTheTenantName(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	r.p.names = nameStub{byID: map[string]string{"t-acme": "acme"}}
	w := r.do(http.MethodPut, "/api/v1/tenants/t-acme/settings/tenant.admission/require-mfa",
		map[string]any{"value": true}, authntest.WithAccount(authn.AccountID("carol")))
	if w.Code != http.StatusNoContent {
		t.Fatalf("the request ended with %d: %s", w.Code, w.Body.String())
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the write wrote %d audit rows, want one mandatory row", len(rows))
	}
	if rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("the mandatory row names the tenant %q, want the name acme", rows[0].Detail[detailTenant])
	}
}
