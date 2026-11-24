package tenantadmin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
)

// engine mounts the module's closed route table on a real gin engine, the way
// the entry point does.
func (r *rig) engine() *gin.Engine {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	for _, rt := range r.p.routes() {
		e.Handle(rt.method, rt.path, withRealm("api", rt.handler))
	}
	return e
}

// send issues one request through the mounted engine with the identity the
// options build.
func (r *rig) send(e *gin.Engine, method, target string, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.sendBody(e, method, target, nil, opts...)
}

// sendBody issues one request through the mounted engine with a JSON body.
func (r *rig) sendBody(e *gin.Engine, method, target string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			r.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(authntest.Context(r.t, req.Context(), opts...))
	e.ServeHTTP(w, req)
	return w
}

// TestTheRouteTableIsTheClosedSet holds the exact (method, path) set and the
// permission each route asks for.
func TestTheRouteTableIsTheClosedSet(t *testing.T) {
	want := map[mountedRoute]authz.Permission{
		{http.MethodGet, "/api/v1/tenants/:tenant/permissions"}:                       authz.PermissionTenantRoleRead,
		{http.MethodGet, "/api/v1/tenants/:tenant/roles"}:                             authz.PermissionTenantRoleRead,
		{http.MethodGet, "/api/v1/tenants/:tenant/roles/:role/bindings"}:              authz.PermissionTenantRoleRead,
		{http.MethodPut, "/api/v1/tenants/:tenant/roles/:role"}:                       authz.PermissionTenantRoleManage,
		{http.MethodDelete, "/api/v1/tenants/:tenant/roles/:role"}:                    authz.PermissionTenantRoleManage,
		{http.MethodGet, "/api/v1/tenants/:tenant/roles/:role/grants"}:                authz.PermissionTenantRoleRead,
		{http.MethodPut, "/api/v1/tenants/:tenant/roles/:role/grants/:permission"}:    authz.PermissionTenantRoleManage,
		{http.MethodDelete, "/api/v1/tenants/:tenant/roles/:role/grants/:permission"}: authz.PermissionTenantRoleManage,
		{http.MethodGet, "/api/v1/tenants/:tenant/bindings"}:                          authz.PermissionTenantRoleRead,
		{http.MethodGet, "/api/v1/tenants/:tenant/bindings/:account"}:                 authz.PermissionTenantRoleRead,
		{http.MethodPut, "/api/v1/tenants/:tenant/bindings/:account/:role"}:           authz.PermissionTenantRoleManage,
		{http.MethodDelete, "/api/v1/tenants/:tenant/bindings/:account/:role"}:        authz.PermissionTenantRoleManage,
	}
	r := newRig(t)
	got := r.p.routes()
	if len(got) != len(want) {
		t.Fatalf("the route table holds %d routes, want %d", len(got), len(want))
	}
	for _, rt := range got {
		perm, ok := want[mountedRoute{rt.method, rt.path}]
		if !ok {
			t.Errorf("unexpected route %s %s", rt.method, rt.path)
			continue
		}
		if rt.perm != perm {
			t.Errorf("%s %s asks for %s, want %s", rt.method, rt.path, rt.perm, perm)
		}
	}
}

// TestEachReadRouteIsWiredToItsOwnHandler holds the path-to-handler wiring: each
// read sent through the mounted table answers the view its path names and no
// other.
func TestEachReadRouteIsWiredToItsOwnHandler(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.manager.grants = []authz.GrantRecord{{Tenant: "t1", Role: "r1", Resource: "account", Action: "read"}}
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}
	e := r.engine()

	cases := map[string]struct {
		path  string
		want  string
		avoid string
	}{
		"the roles list":    {"/api/v1/tenants/t1/roles", "roles", "bindings"},
		"the grants list":   {"/api/v1/tenants/t1/roles/r1/grants", "grants", "roles"},
		"the bindings list": {"/api/v1/tenants/t1/bindings", "bindings", "roles"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := r.send(e, http.MethodGet, tc.path)
			if w.Code != http.StatusOK {
				t.Fatalf("the read answered %d, want 200; body = %q", w.Code, w.Body.String())
			}
			if !hasKey(t, w.Body.String(), tc.want) {
				t.Fatalf("the answer %q does not carry the %q key", w.Body.String(), tc.want)
			}
			if hasKey(t, w.Body.String(), tc.avoid) {
				t.Fatalf("the answer %q carries the %q key: the route is wired to the wrong handler", w.Body.String(), tc.avoid)
			}
		})
	}
}

// TestThePermissionsReadRouteIsWired holds the catalog route through the mounted
// table: it answers the governed permissions at its path and no other.
func TestThePermissionsReadRouteIsWired(t *testing.T) {
	r := newRig(t)
	e := r.engine()
	w := r.send(e, http.MethodGet, "/api/v1/tenants/t1/permissions")
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	if !hasKey(t, w.Body.String(), "permissions") {
		t.Fatalf("the answer %q does not carry the permissions key", w.Body.String())
	}
}

// TestEveryWriteRouteAsksForTenantRoleManage holds the write side of the route
// table's permission mapping: every write sent through the mounted table asks
// the guard for authz.tenant.role.manage.
func TestEveryWriteRouteAsksForTenantRoleManage(t *testing.T) {
	cases := map[string]struct {
		method string
		path   string
		body   any
	}{
		"the role write":     {http.MethodPut, "/api/v1/tenants/t1/roles/editor", roleBody{Description: "Runs the editor."}},
		"the role delete":    {http.MethodDelete, "/api/v1/tenants/t1/roles/editor", nil},
		"the grant write":    {http.MethodPut, "/api/v1/tenants/t1/roles/editor/grants/" + string(authz.PermissionTenantRoleRead), nil},
		"the grant delete":   {http.MethodDelete, "/api/v1/tenants/t1/roles/editor/grants/" + string(authz.PermissionTenantRoleRead), nil},
		"the binding write":  {http.MethodPut, "/api/v1/tenants/t1/bindings/acct-1/editor", nil},
		"the binding delete": {http.MethodDelete, "/api/v1/tenants/t1/bindings/acct-1/editor", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, authz.PermissionTenantRoleRead)
			w := r.sendBody(r.engine(), tc.method, tc.path, tc.body)
			if w.Code != http.StatusNoContent {
				t.Fatalf("the write answered %d, want 204; body = %q", w.Code, w.Body.String())
			}
			if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionTenantRoleManage {
				t.Fatalf("the write asked for %v, want [authz.tenant.role.manage]", got)
			}
		})
	}
}

// TestEveryRouteDecidesInThePathTenantDomain holds the domain every route asks
// the guard to decide in: the tenant the path names, not the platform domain and
// not a fixed unknown domain.
func TestEveryRouteDecidesInThePathTenantDomain(t *testing.T) {
	r := newRig(t)
	w := r.send(r.engine(), http.MethodGet, "/api/v1/tenants/other/roles")
	if w.Code != http.StatusOK {
		t.Fatalf("the read answered %d, want 200", w.Code)
	}
	if got := r.guard.domainsAsked(); len(got) != 1 || got[0] != authz.Domain("other") {
		t.Fatalf("the route decided in %v, want [other]: the tenant the path names", got)
	}
}
