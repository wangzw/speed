package admin

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

func (r *rig) engine() *gin.Engine {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	for _, rt := range r.p.routes() {
		e.Handle(rt.method, rt.path, withRealm("public", rt.handler))
	}
	return e
}

func (r *rig) send(e *gin.Engine, method, target string, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.sendBody(e, method, target, nil, opts...)
}

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

// TestEachReadRouteIsWiredToItsOwnHandler checks that each read route is wired to its own handler.
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
		"the roles list":    {"/api/v1/system/tenants/t1/roles", "roles", "bindings"},
		"the grants list":   {"/api/v1/system/tenants/t1/roles/r1/grants", "grants", "roles"},
		"the bindings list": {"/api/v1/system/tenants/t1/bindings", "bindings", "roles"},
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

// TestThePermissionsReadRouteIsWired checks that the permissions read route is wired.
func TestThePermissionsReadRouteIsWired(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead)
	e := r.engine()
	w := r.send(e, http.MethodGet, "/api/v1/system/permissions")
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	if !hasKey(t, w.Body.String(), "permissions") {
		t.Fatalf("the answer %q does not carry the permissions key", w.Body.String())
	}
}

// TestEveryWriteRouteAsksForRoleManage checks that every write route asks for role manage.
func TestEveryWriteRouteAsksForRoleManage(t *testing.T) {
	cases := map[string]struct {
		method string
		path   string
		body   any
	}{
		"the role write":     {http.MethodPut, "/api/v1/system/tenants/t1/roles/editor", roleBody{Description: "Runs the editor."}},
		"the role delete":    {http.MethodDelete, "/api/v1/system/tenants/t1/roles/editor", nil},
		"the grant write":    {http.MethodPut, "/api/v1/system/tenants/t1/roles/editor/grants/" + string(authz.PermissionAccountRead), nil},
		"the grant delete":   {http.MethodDelete, "/api/v1/system/tenants/t1/roles/editor/grants/" + string(authz.PermissionAccountRead), nil},
		"the binding write":  {http.MethodPut, "/api/v1/system/tenants/t1/bindings/acct-1/editor", nil},
		"the binding delete": {http.MethodDelete, "/api/v1/system/tenants/t1/bindings/acct-1/editor", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, authz.PermissionAccountRead)
			w := r.sendBody(r.engine(), tc.method, tc.path, tc.body)
			if w.Code != http.StatusNoContent {
				t.Fatalf("the write answered %d, want 204; body = %q", w.Code, w.Body.String())
			}
			if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
				t.Fatalf("the write asked for %v, want [authz.role.manage]", got)
			}
		})
	}
}
