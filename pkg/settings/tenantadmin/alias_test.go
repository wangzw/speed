package tenantadmin

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestTheAuditDetailNamesTheTenantAlias holds the write's mandatory row to the
// alias: the path-tenant guard resolved the segment to the internal identifier
// before the handler runs, and a tenant detail carrying that identifier would
// put it in a table operators read.
func TestTheAuditDetailNamesTheTenantAlias(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()
	r.p.aliases = aliasStub{byID: map[string]string{"t-acme": "acme"}}
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
		t.Errorf("the mandatory row names the tenant %q, want the alias acme", rows[0].Detail[detailTenant])
	}
}
