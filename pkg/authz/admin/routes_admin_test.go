package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
)

// engine mounts the module's closed route table on a real gin engine, the way
// the entry point does, so a case can send a request through the routes() wiring
// rather than call a handler directly.
func (r *rig) engine() *gin.Engine {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	for _, rt := range r.p.routes() {
		e.Handle(rt.method, rt.path, withRealm("public", rt.handler))
	}
	return e
}

// send issues one request through the mounted engine with the identity the
// options build.
func (r *rig) send(e *gin.Engine, method, target string, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(authntest.Context(r.t, req.Context(), opts...))
	e.ServeHTTP(w, req)
	return w
}

// TestEachReadRouteIsWiredToItsOwnHandler holds the route table's path-to-handler
// wiring: each read sent through the mounted table answers the view its path
// names and no other. A registration-only case checks (method, path) pairs; the
// roles path answered by the bindings handler, or the grants path by the roles
// handler, passes that and fails here.
func TestEachReadRouteIsWiredToItsOwnHandler(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead)
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.manager.grants = []authz.GrantRecord{{Tenant: "t1", Role: "r1", Resource: "account", Action: "read"}}
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}
	e := r.engine()

	cases := map[string]struct {
		path  string
		want  string
		avoid string
	}{
		"the roles list":    {"/api/v1/authz/admin/tenants/t1/roles", "roles", "bindings"},
		"the grants list":   {"/api/v1/authz/admin/tenants/t1/roles/r1/grants", "grants", "roles"},
		"the bindings list": {"/api/v1/authz/admin/tenants/t1/bindings", "bindings", "roles"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := r.send(e, http.MethodGet, tc.path)
			if w.Code != http.StatusOK {
				t.Fatalf("the read answered %d, want 200; body = %q", w.Code, w.Body.String())
			}
			if !hasKey(t, w.Body.String(), tc.want) {
				t.Fatalf("the answer %q does not carry the %q key: the route is wired to another handler", w.Body.String(), tc.want)
			}
			if hasKey(t, w.Body.String(), tc.avoid) {
				t.Fatalf("the answer %q carries the %q key: the route is wired to the wrong handler", w.Body.String(), tc.avoid)
			}
		})
	}
}

// TestThePermissionsReadRouteIsWired holds the catalog route through the mounted
// table: it answers the permissions view at its path and no other.
func TestThePermissionsReadRouteIsWired(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead)
	e := r.engine()
	w := r.send(e, http.MethodGet, "/api/v1/authz/admin/permissions")
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	if !hasKey(t, w.Body.String(), "permissions") {
		t.Fatalf("the answer %q does not carry the permissions key", w.Body.String())
	}
}
