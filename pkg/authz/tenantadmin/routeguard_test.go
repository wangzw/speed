package tenantadmin

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
)

func (r *rig) mountOn() *gin.Engine {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	for _, rt := range r.p.routes() {
		e.Handle(rt.method, rt.path, withRealm("api", r.p.routeGuard.GuardTenantAdmin(rt.handler)))
	}
	return e
}

// TestEveryRouteIsWrappedWithTheTenantAdminGuard checks that every route is wrapped with the tenant admin guard.
func TestEveryRouteIsWrappedWithTheTenantAdminGuard(t *testing.T) {
	r := newRig(t)
	r.p.mount(newStubRouter("api"))
	if got, want := r.routeGuard.wrapCount(), len(r.p.routes()); got != want {
		t.Fatalf("the tenant administration guard wrapped %d handlers, want %d (one per route)", got, want)
	}
	if got := r.routeGuard.sharedCount(); got != 0 {
		t.Fatalf("the shared path-tenant guard was applied %d times, want the tenant administration guard alone", got)
	}
}

// TestThePathTenantGuardRefusesBeforeTheHandler checks that the path tenant guard refuses before the handler.
func TestThePathTenantGuardRefusesBeforeTheHandler(t *testing.T) {
	r := newRig(t)
	r.routeGuard.refuseWith(http.StatusNotFound)
	e := r.mountOn()

	w := r.send(e, http.MethodGet, "/api/v1/tenants/other/roles")
	if w.Code != http.StatusNotFound {
		t.Fatalf("a mismatched path tenant answered %d, want 404", w.Code)
	}
	if hasKey(t, w.Body.String(), "roles") {
		t.Fatalf("the 404 carried a roles view: %q", w.Body.String())
	}
	if got := r.guard.askedFor(); len(got) != 0 {
		t.Fatalf("the permission guard was asked %v after the path tenant was refused", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v after the path tenant was refused", got)
	}
}

// TestThePathTenantGuardCoversEveryRoute checks that the path tenant guard covers every route.
func TestThePathTenantGuardCoversEveryRoute(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/tenants/other/permissions"},
		{http.MethodPut, "/api/v1/tenants/other/roles/r1"},
		{http.MethodDelete, "/api/v1/tenants/other/roles/r1"},
		{http.MethodPut, "/api/v1/tenants/other/roles/r1/grants/" + string(authz.PermissionTenantRoleRead)},
		{http.MethodDelete, "/api/v1/tenants/other/bindings/acct-1/editor"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := newRig(t, authz.PermissionTenantRoleRead)
			r.routeGuard.refuseWith(http.StatusNotFound)
			w := r.sendBody(r.mountOn(), tc.method, tc.path, nil)
			if w.Code != http.StatusNotFound {
				t.Fatalf("the request answered %d, want 404 from the path-tenant guard", w.Code)
			}
			if got := r.manager.recorded(); len(got) != 0 {
				t.Fatalf("the manager was called %v after the path tenant was refused", got)
			}
		})
	}
}
