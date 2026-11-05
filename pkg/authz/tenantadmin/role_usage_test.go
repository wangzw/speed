package tenantadmin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// viewString reads one JSON member of a decoded view as text.
func viewString(t *testing.T, view map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := view[key]
	if !ok {
		t.Fatalf("the view %v carries no %q member", keysOf(view), key)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		t.Fatalf("the view's %q is not a string: %v", key, err)
	}
	return text
}

// viewInt reads one JSON member of a decoded view as an integer.
func viewInt(t *testing.T, view map[string]json.RawMessage, key string) int {
	t.Helper()
	raw, ok := view[key]
	if !ok {
		t.Fatalf("the view %v carries no %q member", keysOf(view), key)
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("the view's %q is not an integer: %v", key, err)
	}
	return n
}

// viewBool reads one JSON member of a decoded view as a boolean.
func viewBool(t *testing.T, view map[string]json.RawMessage, key string) bool {
	t.Helper()
	raw, ok := view[key]
	if !ok {
		t.Fatalf("the view %v carries no %q member", keysOf(view), key)
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("the view's %q is not a boolean: %v", key, err)
	}
	return b
}

// viewIsNull reports whether the view carries key and its value is JSON null.
func viewIsNull(t *testing.T, view map[string]json.RawMessage, key string) bool {
	t.Helper()
	raw, ok := view[key]
	if !ok {
		t.Fatalf("the view %v carries no %q member", keysOf(view), key)
	}
	return strings.TrimSpace(string(raw)) == "null"
}

// roleViews decodes the role list answer into one map per view.
func roleViews(t *testing.T, body string) []map[string]json.RawMessage {
	t.Helper()
	var got struct {
		Roles []map[string]json.RawMessage `json:"roles"`
	}
	decodeInto(t, body, &got)
	return got.Roles
}

// roleListView sends the role list route and returns its first view.
func roleListView(t *testing.T, r *rig) map[string]json.RawMessage {
	t.Helper()
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	views := roleViews(t, w.Body.String())
	if len(views) != 1 {
		t.Fatalf("the answer carries %d views, want one", len(views))
	}
	return views[0]
}

// TestTheRoleListReportsUsageAndManagement holds the role view's role-usage
// fields: the free-text description, the grant count, the active member count
// and whether the caller may manage the role's grants.
func TestTheRoleListReportsUsageAndManagement(t *testing.T) {
	t.Run("a custom role carries its free text and its permission count", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.manager.roles = []authz.RoleRecord{
			{Tenant: "t1", Role: "editor", DescriptionKey: "authz.role.editor", Description: "Writes posts"},
		}
		r.manager.grants = []authz.GrantRecord{
			{Tenant: "t1", Role: "editor", Resource: "account", Action: "read"},
			{Tenant: "t1", Role: "editor", Resource: "account", Action: "write"},
		}
		view := roleListView(t, r)
		if got := viewString(t, view, "description"); got != "Writes posts" {
			t.Errorf("the description = %q, want the role's free text", got)
		}
		if got := viewInt(t, view, "permission_count"); got != 2 {
			t.Errorf("the permission count = %d, want 2", got)
		}
		if !viewIsNull(t, view, "member_count") {
			t.Errorf("the member count = %s, want null with no identity reader", view["member_count"])
		}
		if viewBool(t, view, "can_manage") {
			t.Errorf("the caller holds no authz.tenant.role.manage, so can_manage must be false")
		}
	})

	t.Run("the description key is kept beside the free text", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.manager.roles = []authz.RoleRecord{
			{Tenant: "t1", Role: "editor", DescriptionKey: "authz.role.editor", Description: "Writes posts"},
		}
		view := roleListView(t, r)
		if got := viewString(t, view, "description_key"); got != "authz.role.editor" {
			t.Errorf("the description key = %q, want the stored key kept for old clients", got)
		}
	})

	t.Run("a caller holding the manage code may manage", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
		r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
		view := roleListView(t, r)
		if !viewBool(t, view, "can_manage") {
			t.Errorf("the caller holds authz.tenant.role.manage, so can_manage must be true")
		}
	})

	t.Run("a builtin role's description is rendered from its key", func(t *testing.T) {
		const key = "authz.role.tenantAdmin"
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: authz.TenantAdminRole, Builtin: true, DescriptionKey: key}}
		r.localizeWith(stubLocalizer{
			defaultLocale: "en-US",
			index: map[i18n.Locale]map[i18n.Key]string{
				"zh-CN": {key: "租户管理员"},
			},
		}, "zh-CN")
		view := roleListView(t, r)
		if got := viewString(t, view, "description"); got != "租户管理员" {
			t.Errorf("the builtin description = %q, want the request language's text", got)
		}
	})

	t.Run("a builtin role's key no catalog carries falls back to the key", func(t *testing.T) {
		const key = "authz.role.untranslated"
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: authz.TenantAdminRole, Builtin: true, DescriptionKey: key}}
		r.localizeWith(stubLocalizer{defaultLocale: "en-US"}, "en-US")
		view := roleListView(t, r)
		if got := viewString(t, view, "description"); got != key {
			t.Errorf("the builtin description = %q, want the key itself %q", got, key)
		}
	})
}

// TestThePermissionCatalogReportsGovernanceMetadata holds the catalog view's
// group, dangerous and grantable members. The governed set is returned whole,
// and grantable is the caller's own held set.
func TestThePermissionCatalogReportsGovernanceMetadata(t *testing.T) {
	perms := []authz.Permission{authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage}
	r := newRig(t, perms...)
	r.catalog.metadata[authz.PermissionTenantRoleRead] = authz.PermissionMeta{Group: "authorization"}
	r.catalog.metadata[authz.PermissionTenantRoleManage] = authz.PermissionMeta{Group: "authorization", Dangerous: true}
	r.subject.answer(authz.PermissionTenantRoleRead)
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
		"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	var got struct {
		Permissions []map[string]json.RawMessage `json:"permissions"`
	}
	decodeInto(t, w.Body.String(), &got)
	if len(got.Permissions) != len(perms) {
		t.Fatalf("the answer lists %d permissions, want the whole governed set of %d", len(got.Permissions), len(perms))
	}
	{
		view := got.Permissions[0]
		if group := viewString(t, view, "group"); group != "authorization" {
			t.Errorf("the first permission's group = %q, want authorization", group)
		}
		if viewBool(t, view, "dangerous") {
			t.Errorf("the first permission is not dangerous")
		}
		if !viewBool(t, view, "grantable") {
			t.Errorf("the caller holds the first permission, so it is grantable")
		}
	}
	{
		view := got.Permissions[1]
		if !viewBool(t, view, "dangerous") {
			t.Errorf("the second permission is dangerous")
		}
		if viewBool(t, view, "grantable") {
			t.Errorf("the caller does not hold the second permission, so it is not grantable")
		}
	}
}

// TestTheRolePutAcceptsFreeTextDescription holds the role write's body: a
// description is accepted, it wins over the legacy key, and one longer than
// authz.RoleDescriptionMax is a 400 answered before the manager.
func TestTheRolePutAcceptsFreeTextDescription(t *testing.T) {
	t.Run("the description alone reaches the manager", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
			params("tenant", "t1", "role", "editor"), map[string]any{"description": "Writes posts"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("writing a role answered %d, want 204; body = %q", w.Code, w.Body.String())
		}
		calls := r.manager.recorded()
		if len(calls) != 1 || calls[0].op != "PutRole" {
			t.Fatalf("the manager calls = %+v, want one PutRole", calls)
		}
		if got := calls[0].roleRec.Description; got != "Writes posts" {
			t.Errorf("the record's description = %q, want the body's text", got)
		}
	})

	t.Run("the description wins over the legacy key", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
			params("tenant", "t1", "role", "editor"),
			map[string]any{"description_key": "authz.role.editor", "description": "Writes posts"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("writing a role answered %d, want 204; body = %q", w.Code, w.Body.String())
		}
		calls := r.manager.recorded()
		if len(calls) != 1 || calls[0].roleRec.Description != "Writes posts" {
			t.Fatalf("the record = %+v, want the description to win", calls)
		}
	})

	t.Run("a description longer than the maximum is a 400", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
			params("tenant", "t1", "role", "editor"),
			map[string]any{"description": strings.Repeat("x", authz.RoleDescriptionMax+1)})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("an over-long description answered %d, want 400", w.Code)
		}
		if got := writes(r.manager.recorded()); len(got) != 0 {
			t.Fatalf("the manager was called %+v for an over-long description", got)
		}
	})

	t.Run("a role name in the body renames nothing", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
			params("tenant", "t1", "role", "editor"),
			map[string]any{"name": "renamed", "description": "Writes posts"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("writing a role answered %d, want 204; body = %q", w.Code, w.Body.String())
		}
		calls := r.manager.recorded()
		if len(calls) != 1 || calls[0].roleRec.Role != authz.Role("editor") {
			t.Fatalf("the record = %+v, want the path's role name", calls)
		}
	})
}

// TestTheRoleBindingsRouteIsMounted holds the role usage read: the path the
// role page's member list uses answers a page of the role's bindings.
func TestTheRoleBindingsRouteIsMounted(t *testing.T) {
	r := newRig(t)
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}
	w := r.send(r.engine(), http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings")
	if w.Code != http.StatusOK {
		t.Fatalf("the role bindings route answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	if !hasKey(t, w.Body.String(), "bindings") {
		t.Fatalf("the answer %q does not carry the bindings key", w.Body.String())
	}
	if !hasKey(t, w.Body.String(), "next_cursor") {
		t.Fatalf("the answer %q does not carry the next_cursor key", w.Body.String())
	}
}
