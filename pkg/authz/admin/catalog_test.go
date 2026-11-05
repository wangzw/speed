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

// TestTheCatalogRouteListsDeclaredPermissions drives GET /api/v1/system/permissions
// over the shared rig: the set the catalog declares appears in the answer
// verbatim, one view per permission and in the order the catalog gave them, and
// each view carries the one field the route declares and nothing else. The
// answer is a view built field by field, never the catalog value handed through
// as it stands, so a field added to the record later cannot reach a client by
// being there.
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
		// The rig assembles no message localizer, so the description is the
		// empty string rather than the key: with nothing to render through the
		// route makes no claim about text it does not have.
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

// permissionViews decodes the catalog route's answer into one map per view. The
// members are kept raw because the view mixes text and booleans.
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

// viewString decodes one string member of a raw view.
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

// viewBool decodes one boolean member of a raw view.
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

// TestTheCatalogRouteRendersTheDescriptionInTheRequestLocale drives the catalog
// route with a localizer assembled and the same request in two languages: the
// description is the text the declaring module gave the permission's key,
// rendered in the locale the request carries. The route adds no text of its own,
// so a permission in a language the catalogue carries comes back in that
// language and not in another.
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

// TestTheCatalogRouteAnswersNoDescriptionWhenThereIsNothingToRender holds the
// fallback rule for the two cases where the route has no text: the assembly
// carries no localizer, and the declaring module gave the permission no key.
// Both answer an empty description, never the key and never another
// permission's text.
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

// TestTheCatalogRouteWritesBackAKeyNoCatalogCarries holds the engine's fallback:
// a declaring module gave the permission a key, no catalogue carries text for
// it, and the localizer writes the key itself back, so the field says the text
// is missing rather than claiming there is none. A client shows what it received
// and does not resolve keys of its own.
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

// TestTheCatalogRouteCarriesTheMetadataAndTheCallersHeldSet holds the catalog
// view's remaining fields: group and dangerous come from the declaring module's
// metadata, and grantable is the caller's own held permission in the platform
// domain. A permission the caller does not hold is still listed, marked not
// grantable; a permission with no metadata answers an empty group and a false
// dangerous mark.
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

// TestTheCatalogRouteWhoseHeldSetCannotBeReadIsA503 holds the failure rule: a
// held set that cannot be read is a 503, never a catalog whose every permission
// quietly says it cannot be granted.
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

// TestTheCatalogRouteRequiresRoleRead holds the route's decision: the handler
// asks the guard for authz.role.read, and a refusal ends the request before the
// catalog is read, leaving the declared set nowhere in the answer and no
// downstream call recorded.
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
		// A read ends after the decision: it calls the catalog, not the
		// manager, and lands no mandatory audit row, which only a write carries.
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

// TestTheCatalogRouteNamesNoOpaqueID holds that the catalog route exposes no
// opaque identifier: its declared path carries no parameter segment, so a row
// is never addressed by an identifier standing in a path (the way the import
// command exposes no account parameter a caller could name), and the view names
// each permission by the permission string itself and by no id.
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
