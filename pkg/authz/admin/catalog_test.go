package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// TestTheCatalogRouteListsDeclaredPermissions checks that the catalog route lists declared permissions.
func TestTheCatalogRouteListsDeclaredPermissions(t *testing.T) {
	declared := []authz.Permission{
		authz.PermissionRoleRead,
		authz.PermissionRoleManage,
		authz.PermissionAccountRead,
	}
	r := newRig(t, declared...)
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200", w.Code)
	}
	var got struct {
		Permissions []map[string]json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("the answer %q is not a permissions object: %v", w.Body.String(), err)
	}
	if len(got.Permissions) != len(declared) {
		t.Fatalf("the answer lists %d permissions, want the %d the catalog declares", len(got.Permissions), len(declared))
	}
	for i, want := range declared {
		view := got.Permissions[i]
		raw, ok := view["permission"]
		if !ok {
			t.Fatalf("view %d carries no permission field: %+v", i, view)
		}
		var named string
		if err := json.Unmarshal(raw, &named); err != nil {
			t.Fatalf("view %d's permission is not a string: %v", i, err)
		}
		if named != string(want) {
			t.Errorf("view %d names %q, want the declared permission %q in order", i, named, want)
		}
		if raw, ok := view["description"]; !ok {
			t.Errorf("view %d carries no description field: %+v", i, view)
		} else {
			var description string
			if err := json.Unmarshal(raw, &description); err != nil {
				t.Errorf("view %d's description is not a string: %v", i, err)
			} else if description != "" {
				t.Errorf("view %d's description = %q with no localizer assembled, want the empty string", i, description)
			}
		}
		if len(view) != 5 {
			t.Errorf("view %d carries %d fields (%v), want the five fields the route declares", i, len(view), view)
		}
	}
}

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

func viewString(t *testing.T, view map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := view[key]
	if !ok {
		t.Fatalf("the view carries no %q member: %v", key, view)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("the view's %q member is not a string: %v", key, err)
	}
	return value
}

func viewBool(t *testing.T, view map[string]json.RawMessage, key string) bool {
	t.Helper()
	raw, ok := view[key]
	if !ok {
		t.Fatalf("the view carries no %q member: %v", key, view)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("the view's %q member is not a boolean: %v", key, err)
	}
	return value
}

// TestTheCatalogRouteRendersTheDescriptionInTheRequestLocale checks that the catalog route renders the description in the request locale.
func TestTheCatalogRouteRendersTheDescriptionInTheRequestLocale(t *testing.T) {
	const (
		readKey   = "authz.permission.authz-role-read"
		manageKey = "authz.permission.authz-role-manage"
	)
	en := i18n.Locale("en-US")
	zh := i18n.Locale("zh-CN")
	messages := stubLocalizer{
		defaultLocale: en,
		index: map[i18n.Locale]map[i18n.Key]string{
			en: {
				readKey:   "Read roles",
				manageKey: "Manage roles",
			},
			zh: {
				readKey:   "读取角色",
				manageKey: "管理角色",
			},
		},
	}
	want := map[i18n.Locale][]string{
		en: {"Read roles", "Manage roles"},
		zh: {"读取角色", "管理角色"},
	}
	for locale, texts := range want {
		t.Run(string(locale), func(t *testing.T) {
			r := newRig(t, authz.PermissionRoleRead, authz.PermissionRoleManage)
			r.catalog.describe(authz.PermissionRoleRead, readKey)
			r.catalog.describe(authz.PermissionRoleManage, manageKey)
			r.localizeWith(messages, locale)
			w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("the catalog route answered %d, want 200", w.Code)
			}
			views := permissionViews(t, w.Body.String())
			if len(views) != len(texts) {
				t.Fatalf("the answer lists %d views, want %d", len(views), len(texts))
			}
			for i, text := range texts {
				if got := viewString(t, views[i], "description"); got != text {
					t.Errorf("view %d's description = %q in %s, want the text the key carries %q", i, got, locale, text)
				}
			}
		})
	}
}

// TestTheCatalogRouteAnswersNoDescriptionWhenThereIsNothingToRender checks that the catalog route answers no description when there is nothing to render.
func TestTheCatalogRouteAnswersNoDescriptionWhenThereIsNothingToRender(t *testing.T) {
	t.Run("no localizer assembled", func(t *testing.T) {
		r := newRig(t, authz.PermissionRoleRead)
		r.catalog.describe(authz.PermissionRoleRead, "authz.permission.authz-role-read")
		w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
		views := permissionViews(t, w.Body.String())
		if len(views) != 1 || viewString(t, views[0], "description") != "" {
			t.Fatalf("the answer = %+v, want one view whose description is the empty string", views)
		}
	})
	t.Run("the declared permission carries no description key", func(t *testing.T) {
		r := newRig(t, authz.PermissionRoleRead)
		r.localizeWith(stubLocalizer{
			defaultLocale: "en-US",
			index: map[i18n.Locale]map[i18n.Key]string{
				"en-US": {"authz.permission.other": "Another permission"},
			},
		}, "en-US")
		w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
		views := permissionViews(t, w.Body.String())
		if len(views) != 1 || viewString(t, views[0], "description") != "" {
			t.Fatalf("the answer = %+v, want one view whose description is the empty string", views)
		}
	})
}

// TestTheCatalogRouteWritesBackAKeyNoCatalogCarries checks that the catalog route writes back a key no catalog carries.
func TestTheCatalogRouteWritesBackAKeyNoCatalogCarries(t *testing.T) {
	const key = "tenant.permission.untranslated"
	r := newRig(t, authz.PermissionTenantAccess)
	r.catalog.describe(authz.PermissionTenantAccess, key)
	r.localizeWith(stubLocalizer{defaultLocale: "en-US"}, "en-US")
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
	views := permissionViews(t, w.Body.String())
	if len(views) != 1 {
		t.Fatalf("the answer lists %d views, want one", len(views))
	}
	if got := viewString(t, views[0], "description"); got != key {
		t.Fatalf("the description = %q for a key no catalogue carries, want the key itself %q", got, key)
	}
}

// TestTheCatalogRouteCarriesTheMetadataAndTheCallersHeldSet checks that the catalog route carries the metadata and the callers held set.
func TestTheCatalogRouteCarriesTheMetadataAndTheCallersHeldSet(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead, authz.PermissionRoleManage, authz.PermissionAccountRead)
	r.catalog.metadata[authz.PermissionRoleRead] = authz.PermissionMeta{Group: "authorization"}
	r.catalog.metadata[authz.PermissionRoleManage] = authz.PermissionMeta{Group: "authorization", Dangerous: true}
	r.subject.answer(authz.PermissionRoleRead)
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200", w.Code)
	}
	views := permissionViews(t, w.Body.String())
	if len(views) != 3 {
		t.Fatalf("the answer lists %d views, want three", len(views))
	}
	if got := viewString(t, views[0], "group"); got != "authorization" {
		t.Errorf("the held permission's group = %q, want the module's group", got)
	}
	if viewBool(t, views[0], "dangerous") {
		t.Error("the held permission is marked dangerous, want the module's false")
	}
	if !viewBool(t, views[0], "grantable") {
		t.Error("a permission the caller holds is marked not grantable")
	}
	if got := viewString(t, views[1], "group"); got != "authorization" {
		t.Errorf("the dangerous permission's group = %q, want the module's group", got)
	}
	if !viewBool(t, views[1], "dangerous") {
		t.Error("the module marked the permission dangerous, but the view does not carry it")
	}
	if viewBool(t, views[1], "grantable") {
		t.Error("a permission the caller does not hold is marked grantable")
	}
	if got := viewString(t, views[2], "group"); got != "" {
		t.Errorf("a permission with no metadata answered group %q, want the empty string", got)
	}
	if viewBool(t, views[2], "dangerous") || viewBool(t, views[2], "grantable") {
		t.Error("a permission with no metadata and no held grant answered a true mark")
	}
	calls := r.subject.asked()
	if len(calls) != 1 || calls[0].domain != authz.PlatformDomain {
		t.Fatalf("the held set was read %+v, want one read in the platform domain", calls)
	}
}

// TestTheCatalogRouteWhoseHeldSetCannotBeReadIsA503 checks that the catalog route whose held set cannot be read is a 503.
func TestTheCatalogRouteWhoseHeldSetCannotBeReadIsA503(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead)
	r.subject.fail(errors.New("the permission read is down"))
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a catalog read whose held set could not be read answered %d, want 503", w.Code)
	}
	if strings.Contains(w.Body.String(), `"permissions"`) {
		t.Fatalf("a failed catalog read carried the permission list: %q", w.Body.String())
	}
}

// TestTheCatalogRouteRequiresRoleRead checks that the catalog route requires role read.
func TestTheCatalogRouteRequiresRoleRead(t *testing.T) {
	t.Run("the route asks the guard for role.read", func(t *testing.T) {
		r := newRig(t, authz.PermissionRoleRead)
		w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
		if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
			t.Fatalf("the catalog route asked the guard for %v, want [authz.role.read]", got)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("an allowed read answered %d, want 200", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Errorf("the manager was called %v on an allowed catalog read", got)
		}
		if got := r.audit.entries(); len(got) != 0 {
			t.Errorf("an allowed catalog read wrote %d audit rows, want none", len(got))
		}
	})
	t.Run("a refusal is a 403 with nothing downstream", func(t *testing.T) {
		r := newRig(t, authz.PermissionRoleRead)
		r.guard.allow = false
		w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("a refused catalog read answered %d, want 403", w.Code)
		}
		if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
			t.Fatalf("the route asked the guard for %v before refusing, want [authz.role.read]", got)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v on a refused read", got)
		}
		if got := r.catalog.asked(); len(got) != 0 {
			t.Fatalf("the catalog was queried %v on a refused read", got)
		}
		if strings.Contains(w.Body.String(), `"permissions"`) {
			t.Fatalf("a refused read carried the permission list: %q", w.Body.String())
		}
	})
}

// TestTheCatalogRouteNamesNoOpaqueID checks that the catalog route names no opaque ID.
func TestTheCatalogRouteNamesNoOpaqueID(t *testing.T) {
	if pathPermissions != "/api/v1/system/permissions" {
		t.Fatalf("the catalog path = %q, want the literal /api/v1/system/permissions", pathPermissions)
	}
	for _, segment := range strings.Split(pathPermissions, "/") {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			t.Fatalf("the catalog path %q carries the parameter segment %q: the management face names a row by a natural key, never an opaque identifier", pathPermissions, segment)
		}
	}

	r := newRig(t, authz.PermissionRoleRead)
	mounted := func() bool {
		for _, rt := range r.p.routes() {
			if rt.method == http.MethodGet && rt.path == pathPermissions {
				return true
			}
		}
		return false
	}
	if !mounted() {
		t.Fatalf("the module declares no GET route at %s", pathPermissions)
	}
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, pathPermissions, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d with no path parameter, want 200", w.Code)
	}
	var got struct {
		Permissions []map[string]json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("the answer %q is not a permissions object: %v", w.Body.String(), err)
	}
	if len(got.Permissions) == 0 {
		t.Fatal("the answer lists no view, so the no-id claim is left unchecked")
	}
	for i, view := range got.Permissions {
		for key := range view {
			if strings.EqualFold(key, "id") || strings.HasSuffix(strings.ToLower(key), "_id") {
				t.Errorf("view %d names the opaque identifier %q: the permission string is the key", i, key)
			}
		}
	}
}
