package tenantadmin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// TestTheCatalogRouteListsOnlyTheGovernedSet holds the catalog read's
// defining rule: it lists Catalog.TenantGoverned() and never
// Catalog.Declared().
func TestTheCatalogRouteListsOnlyTheGovernedSet(t *testing.T) {
	governed := []authz.Permission{authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage}
	declared := append(append([]authz.Permission(nil), governed...), authz.PermissionAccountRead)
	r := newRig(t, declared...) // newRig governs the tenant role codes among them
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
		"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200", w.Code)
	}
	var got struct {
		Permissions []map[string]json.RawMessage `json:"permissions"`
	}
	decodeInto(t, w.Body.String(), &got)
	if len(got.Permissions) != len(governed) {
		t.Fatalf("the answer lists %d permissions, want the %d governed", len(got.Permissions), len(governed))
	}
	for i, want := range governed {
		var named string
		decodeInto(t, string(got.Permissions[i]["permission"]), &named)
		if named != string(want) {
			t.Errorf("view %d names %q, want %q", i, named, want)
		}
		if _, ok := got.Permissions[i]["description"]; !ok {
			t.Errorf("view %d carries no description field", i)
		}
		if len(got.Permissions[i]) != 5 {
			t.Errorf("view %d carries %d fields, want the five the route declares", i, len(got.Permissions[i]))
		}
	}
	for _, view := range got.Permissions {
		var named string
		decodeInto(t, string(view["permission"]), &named)
		if named == string(authz.PermissionAccountRead) {
			t.Fatalf("the answer carries the ungoverned permission %q", named)
		}
	}
}

// permissionViews decodes the catalog route's answer into one map per view. The
// members are kept raw because the view mixes text, booleans and integers.
func permissionViews(t *testing.T, body string) []map[string]json.RawMessage {
	t.Helper()
	var got struct {
		Permissions []map[string]json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("the answer %q is not a permissions object: %v", body, err)
	}
	return got.Permissions
}

// TestTheCatalogRouteRendersTheDescriptionInTheRequestLocale drives the catalog
// route with a localizer assembled and the same request in two languages.
func TestTheCatalogRouteRendersTheDescriptionInTheRequestLocale(t *testing.T) {
	const (
		readKey   = "authz.permission.authz-tenant-role-read"
		manageKey = "authz.permission.authz-tenant-role-manage"
	)
	en := i18n.Locale("en-US")
	zh := i18n.Locale("zh-CN")
	messages := stubLocalizer{
		defaultLocale: en,
		index: map[i18n.Locale]map[i18n.Key]string{
			en: {readKey: "Read reasons", manageKey: "Manage reasons"},
			zh: {readKey: "读取角色", manageKey: "管理角色"},
		},
	}
	want := map[i18n.Locale][]string{
		en: {"Read reasons", "Manage reasons"},
		zh: {"读取角色", "管理角色"},
	}
	for locale, texts := range want {
		t.Run(string(locale), func(t *testing.T) {
			r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
			r.catalog.describe(authz.PermissionTenantRoleRead, readKey)
			r.catalog.describe(authz.PermissionTenantRoleManage, manageKey)
			r.localizeWith(messages, locale)
			w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
				"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
			if w.Code != http.StatusOK {
				t.Fatalf("the catalog route answered %d, want 200", w.Code)
			}
			views := permissionViews(t, w.Body.String())
			if len(views) != len(texts) {
				t.Fatalf("the answer lists %d views, want %d", len(views), len(texts))
			}
			for i, text := range texts {
				if got := viewString(t, views[i], "description"); got != text {
					t.Errorf("view %d's description = %q in %s, want %q", i, got, locale, text)
				}
			}
		})
	}
}

// TestTheCatalogRouteAnswersNoDescriptionWhenThereIsNothingToRender holds the
// fallback rule for the two cases where the route has no text.
func TestTheCatalogRouteAnswersNoDescriptionWhenThereIsNothingToRender(t *testing.T) {
	t.Run("no localizer assembled", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.catalog.describe(authz.PermissionTenantRoleRead, "authz.permission.authz-tenant-role-read")
		w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
			"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
		views := permissionViews(t, w.Body.String())
		if len(views) != 1 || viewString(t, views[0], "description") != "" {
			t.Fatalf("the answer = %+v, want one view whose description is the empty string", views)
		}
	})
	t.Run("the declared permission carries no description key", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.localizeWith(stubLocalizer{defaultLocale: "en-US"}, "en-US")
		w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
			"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
		views := permissionViews(t, w.Body.String())
		if len(views) != 1 || viewString(t, views[0], "description") != "" {
			t.Fatalf("the answer = %+v, want one view whose description is the empty string", views)
		}
	})
}

// TestTheCatalogRouteWritesBackAKeyNoCatalogCarries holds the engine's fallback.
func TestTheCatalogRouteWritesBackAKeyNoCatalogCarries(t *testing.T) {
	const key = "tenant.permission.untranslated"
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.catalog.describe(authz.PermissionTenantRoleRead, key)
	r.localizeWith(stubLocalizer{defaultLocale: "en-US"}, "en-US")
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
		"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
	views := permissionViews(t, w.Body.String())
	if len(views) != 1 || viewString(t, views[0], "description") != key {
		t.Fatalf("the description = %+v, want the key itself %q", views, key)
	}
}

// TestTheCatalogRouteRequiresTenantRoleRead holds the route's decision.
func TestTheCatalogRouteRequiresTenantRoleRead(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
		"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("an allowed read answered %d, want 200", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionTenantRoleRead {
		t.Fatalf("the route asked for %v, want [authz.tenant.role.read]", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Errorf("the manager was called %v on a catalog read", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("the catalog read wrote %d audit rows, want none", len(got))
	}
}

// TestTheViewsExposeNoOpaqueID holds that each view's JSON key set is exactly
// the design's natural-key set, with no opaque identifier.
func TestTheViewsExposeNoOpaqueID(t *testing.T) {
	cases := map[string]struct {
		view any
		want []string
	}{
		"permission":   {AuthzTenantAdminPermissionView{}, []string{"dangerous", "description", "grantable", "group", "permission"}},
		"role":         {AuthzTenantAdminRoleView{}, []string{"builtin", "can_manage", "created_at", "description", "member_count", "permission_count", "role", "tenant"}},
		"role binding": {AuthzTenantAdminRoleBindingView{}, []string{"account", "active", "display_name", "granted_at", "username"}},
		"grant":        {AuthzTenantAdminGrantView{}, []string{"action", "created_at", "permission", "resource", "role", "tenant"}},
		"binding":      {AuthzTenantAdminBindingView{}, []string{"account", "granted_at", "role", "tenant"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(tc.view)
			if err != nil {
				t.Fatalf("marshalling the %s view: %v", name, err)
			}
			got := keysOf(object(t, string(raw)))
			if len(got) != len(tc.want) {
				t.Fatalf("the %s view's fields = %v, want %v", name, got, tc.want)
			}
			for _, want := range tc.want {
				found := false
				for _, key := range got {
					if key == want {
						found = true
					}
					if key == "id" || key == "_id" {
						t.Errorf("the %s view exposes the opaque identifier %q", name, key)
					}
				}
				if !found {
					t.Errorf("the %s view's fields = %v, want %q", name, got, want)
				}
			}
		})
	}
}
