package tenantadmin

import (
	"net/http"
	"testing"
)

// TestTheTenantSettingsRoutesAreMountedAndAnswer checks that all three routes
// are mounted and answer with the layer fixed to the path tenant.
func TestTheTenantSettingsRoutesAreMountedAndAnswer(t *testing.T) {
	r := newRig(t)
	r.store.declared = admissionDeclared()

	if got := len(r.p.routes()); got != 3 {
		t.Fatalf("the module mounts %d routes, want 3", got)
	}

	if w := r.do(http.MethodGet, base, nil); w.Code != http.StatusOK {
		t.Fatalf("the list ended with %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodGet, base+"/tenant.admission", nil); w.Code != http.StatusNotFound {
		t.Fatalf("the removed namespace read ended with %d, want 404", w.Code)
	}
	if w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa", map[string]any{"value": true}); w.Code != http.StatusNoContent {
		t.Fatalf("the write ended with %d, want 204: %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodDelete, base+"/tenant.admission/require-mfa", nil); w.Code != http.StatusNoContent {
		t.Fatalf("the delete ended with %d, want 204: %s", w.Code, w.Body.String())
	}
}
