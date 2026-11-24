package tenantadmin

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestTheViewsNameTheTenantName holds the tenant management views to the
// name: the policy store keys every row by the internal identifier, and a
// client reads the name.
func TestTheViewsNameTheTenantName(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.withNames(map[string]string{"t1": "acme"})
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.manager.grants = []authz.GrantRecord{{Tenant: "t1", Role: "editor", Resource: "tenant_role", Action: "read"}}
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}

	roles := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if roles.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200; body = %q", roles.Code, roles.Body.String())
	}
	var roleBody struct {
		Roles []AuthzTenantAdminRoleView `json:"roles"`
	}
	decodeInto(t, roles.Body.String(), &roleBody)
	if len(roleBody.Roles) != 1 || roleBody.Roles[0].Tenant != "acme" {
		t.Errorf("the role view names %q, want the name acme", roleBody.Roles[0].Tenant)
	}

	grants := r.serve(readRoute(r.p, grantListOperation.Handle(r.p.handleGrantList)), http.MethodGet,
		"/api/v1/tenants/t1/roles/editor/grants", params("tenant", "t1", "role", "editor"), nil)
	if grants.Code != http.StatusOK {
		t.Fatalf("listing the grants answered %d, want 200; body = %q", grants.Code, grants.Body.String())
	}
	var grantBody struct {
		Grants []AuthzTenantAdminGrantView `json:"grants"`
	}
	decodeInto(t, grants.Body.String(), &grantBody)
	if len(grantBody.Grants) != 1 || grantBody.Grants[0].Tenant != "acme" {
		t.Errorf("the grant view names %q, want the name acme", grantBody.Grants[0].Tenant)
	}

	bindings := r.serve(readRoute(r.p, bindingListOperation.Handle(r.p.handleBindingList)), http.MethodGet,
		"/api/v1/tenants/t1/bindings", params("tenant", "t1"), nil)
	if bindings.Code != http.StatusOK {
		t.Fatalf("listing the bindings answered %d, want 200; body = %q", bindings.Code, bindings.Body.String())
	}
	var bindingBody struct {
		Bindings []AuthzTenantAdminBindingView `json:"bindings"`
	}
	decodeInto(t, bindings.Body.String(), &bindingBody)
	if len(bindingBody.Bindings) != 1 || bindingBody.Bindings[0].Tenant != "acme" {
		t.Errorf("the binding view names %q, want the name acme", bindingBody.Bindings[0].Tenant)
	}
}

// TestTheAuditDetailNamesTheTenantName holds the writes' mandatory audit row to
// the name: the path resolved to the internal identifier before the handler
// runs, and a tenant detail carrying that identifier would put it in a table
// operators read.
func TestTheAuditDetailNamesTheTenantName(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleManage)
	r.withNames(map[string]string{"t1": "acme"})
	w := r.serve(rolePutRoute(r.p), http.MethodPut,
		"/api/v1/tenants/t1/roles/editor", params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusNoContent {
		t.Fatalf("the write answered %d, want 204; body = %q", w.Code, w.Body.String())
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the audit rows = %+v, want one mandatory row", rows)
	}
	if rows[0].Detail[detailTenant] != "acme" {
		t.Errorf("the audit detail names the tenant %q, want the name acme", rows[0].Detail[detailTenant])
	}
}
