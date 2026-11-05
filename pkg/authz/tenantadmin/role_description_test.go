package tenantadmin

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestTheRoleWriteMapsTheDescriptionMembersIndependently holds the incremental
// contract on the tenant surface: a client written before the free-text change
// still writes successfully with description_key alone, and the two members are
// mapped independently — the legacy key carries no description, and a
// free-text description carries no key.
func TestTheRoleWriteMapsTheDescriptionMembersIndependently(t *testing.T) {
	t.Run("the legacy key alone is still served", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleManage)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/ops",
			params("tenant", "t1", "role", "ops"), map[string]any{"description_key": "authz.role.ops"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("a legacy key-only write answered %d, want 204; body = %s", w.Code, w.Body.String())
		}
		got := r.manager.recorded()
		if len(got) != 1 || got[0].op != "PutRole" {
			t.Fatalf("the manager calls = %+v, want one PutRole", got)
		}
		rec := got[0].roleRec
		if rec.DescriptionKey != "authz.role.ops" || rec.Description != "" {
			t.Errorf("the record = %+v, want the legacy key forwarded and no description written", rec)
		}
	})

	t.Run("the free text alone writes no key", func(t *testing.T) {
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
			t.Errorf("the record = %+v, want the free text forwarded and no legacy key written", rec)
		}
	})
}
