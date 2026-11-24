package tenantadmin

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestTheRoleWriteCarriesTheFreeTextDescription checks that the role write carries the free text description.
func TestTheRoleWriteCarriesTheFreeTextDescription(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleManage)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"), map[string]any{"description": "Runs the operations."})
	if w.Code != http.StatusNoContent {
		t.Fatalf("a description-only write answered %d, want 204; body = %s", w.Code, w.Body.String())
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole", got)
	}
	rec := got[0].roleRec
	if rec.Description != "Runs the operations." || rec.DescriptionKey != "" {
		t.Errorf("the record = %+v, want the free text forwarded and no key written", rec)
	}
}

// TestTheRoleWriteIgnoresTheRemovedDescriptionKey checks that the role write ignores the removed description key.
func TestTheRoleWriteIgnoresTheRemovedDescriptionKey(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleManage)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"), map[string]any{"description_key": "authz.role.ops"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("a removed-member write answered %d, want 204; body = %s", w.Code, w.Body.String())
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole", got)
	}
	rec := got[0].roleRec
	if rec.DescriptionKey != "" || rec.Description != "" {
		t.Errorf("the record = %+v, want neither a key nor a description", rec)
	}
}
