package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestTheCatalogRouteListsDeclaredPermissions drives GET /api/v1/authz/admin/permissions
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
	w := r.serve(r.p.handlePermissionList, http.MethodGet, "/api/v1/authz/admin/permissions", nil, nil)
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
		if len(view) != 1 {
			t.Errorf("view %d carries %d fields (%v), want the one field the route declares", i, len(view), view)
		}
	}
}

// TestTheCatalogRouteRequiresRoleRead holds the route's decision: the handler
// asks the guard for authz.role.read, and a refusal ends the request before the
// catalog is read, leaving the declared set nowhere in the answer and no
// downstream call recorded.
func TestTheCatalogRouteRequiresRoleRead(t *testing.T) {
	t.Run("the route asks the guard for role.read", func(t *testing.T) {
		r := newRig(t, authz.PermissionRoleRead)
		w := r.serve(r.p.handlePermissionList, http.MethodGet, "/api/v1/authz/admin/permissions", nil, nil)
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
		w := r.serve(r.p.handlePermissionList, http.MethodGet, "/api/v1/authz/admin/permissions", nil, nil)
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
	if pathPermissions != "/api/v1/authz/admin/permissions" {
		t.Fatalf("the catalog path = %q, want the literal /api/v1/authz/admin/permissions", pathPermissions)
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
	w := r.serve(r.p.handlePermissionList, http.MethodGet, pathPermissions, nil, nil)
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
