package pathguard_test

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
	"github.com/vislake/speed/pkg/tenant/pathguard"
)

// roster is a tenant.Catalog answering the two lookups the guard makes. The rest
// is refused, so a guard that reaches for another method fails the case instead
// of silently reading nothing.
type roster struct {
	byName map[string]tenant.Tenant
	byID   map[tenant.ID]tenant.Tenant
}

func newRoster(tenants ...tenant.Tenant) *roster {
	r := &roster{byName: map[string]tenant.Tenant{}, byID: map[tenant.ID]tenant.Tenant{}}
	for _, t := range tenants {
		r.byName[t.Name] = t
		r.byID[t.ID] = t
	}
	return r
}

func (r *roster) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	t, ok := r.byName[name]
	return t, ok, nil
}

func (r *roster) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	t, ok := r.byID[id]
	return t, ok, nil
}

func (*roster) Put(context.Context, tenant.Tenant) error { return errUnused }
func (*roster) Create(context.Context, tenant.Tenant) error {
	return errUnused
}
func (*roster) SetStatus(context.Context, tenant.ID, tenant.Status) error { return errUnused }
func (*roster) SetStatusByAccount(context.Context, tenant.ID, tenant.Status) error {
	return errUnused
}
func (*roster) Delete(context.Context, tenant.ID) error           { return errUnused }
func (*roster) SetOwner(context.Context, tenant.ID, string) error { return errUnused }
func (*roster) SetOwnerIfEmpty(context.Context, tenant.ID, string) (bool, error) {
	return false, errUnused
}

func (*roster) SetProfile(context.Context, tenant.ID, tenant.Profile) error {
	return errUnused
}
func (*roster) List(context.Context) ([]tenant.Tenant, error) { return nil, errUnused }
func (*roster) Enter(context.Context, tenant.ID) (context.Context, error) {
	return nil, errUnused
}

func (*roster) EnterRegistered(context.Context, tenant.ID) (context.Context, error) {
	return nil, errUnused
}

// errUnused is what every roster method the guard does not call answers with.
var errUnused = errUnusedError{}

type errUnusedError struct{}

func (errUnusedError) Error() string { return "the guard called a roster method it does not use" }

// standardRoster names every tenant so that its name differs from its
// identifier: the request surface carries the name, and a segment holding the
// identifier is not a name any tenant is addressed by.
func standardRoster() *roster {
	return newRoster(
		tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive},
		tenant.Tenant{ID: "t_beta", Name: "beta", Status: tenant.StatusActive},
		tenant.Tenant{ID: "system", Name: "platform", Status: tenant.StatusActive},
	)
}

// wrapFunc is the seam both constructors deliver: a function that wraps one
// handler.
type wrapFunc func(gin.HandlerFunc) gin.HandlerFunc

// engine is a router with one tenant-addressed route and one route outside the
// tenant scope, each wrapped by wrap, and, when resolved is not empty, a layer
// that binds the tenant a request resolved to, as the resolving layer does.
func engine(resolved tenant.ID, wrap wrapFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		if resolved != "" {
			c.Request = c.Request.WithContext(tenantctx.WithID(c.Request.Context(), string(resolved)))
		}
		c.Next()
	})
	ok := func(c *gin.Context) { c.Status(nethttp.StatusOK) }
	e.GET("/tenants/:tenant/members", wrap(ok))
	e.GET("/members", wrap(ok))
	return e
}

// sharedEngine mounts the whole group behind the shared identity guard the
// mounter applies to every tenant-scoped group.
func sharedEngine(resolved tenant.ID, r tenant.Catalog) *gin.Engine {
	return engine(resolved, pathguard.New(r).Guard)
}

// tenantAdminEngine mounts a tenant management group behind the stricter guard
// those modules apply themselves.
func tenantAdminEngine(resolved tenant.ID, r tenant.Catalog) *gin.Engine {
	guard := pathguard.NewTenantAdmin(r)
	return engine(resolved, guard.GuardTenantAdmin)
}

func send(e *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(nethttp.MethodGet, path, nil))
	return w
}

// TestTheSharedGuardComparesTenantIdentifiers holds the shared rule the mounter
// applies to every tenant-scoped group: the path segment is resolved through
// the roster and compared by identifier, so the segment may carry the
// identifier or the current name, and any other outcome is the bare 404.
func TestTheSharedGuardComparesTenantIdentifiers(t *testing.T) {
	cases := []struct {
		name     string
		resolved tenant.ID
		path     string
		status   int
	}{
		{"the path carries the current name", "t_acme", "/tenants/acme/members", nethttp.StatusOK},
		{"the path name belongs to another tenant", "t_acme", "/tenants/beta/members", nethttp.StatusNotFound},
		{"the path carries the tenant identifier", "t_acme", "/tenants/t_acme/members", nethttp.StatusNotFound},
		{"the path names another tenant by identifier", "t_acme", "/tenants/t_beta/members", nethttp.StatusNotFound},
		{"the segment names no tenant", "t_acme", "/tenants/ghost/members", nethttp.StatusNotFound},
		{"a route outside the tenant scope passes through", "t_acme", "/members", nethttp.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := send(sharedEngine(tc.resolved, standardRoster()), tc.path)
			if got.Code != tc.status {
				t.Fatalf("%s answered %d, want %d (body %q)", tc.path, got.Code, tc.status, got.Body.String())
			}
			if tc.status == nethttp.StatusNotFound {
				requireBare404(t, got)
			}
		})
	}
}

// TestTheSharedGuardDoesNotClaimTheTenantAdminCapability holds the interface
// split: an assembly that resolves the shared guard cannot type-assert it into
// the tenant administration capability and apply a guard that omits the
// platform-domain refusal.
func TestTheSharedGuardDoesNotClaimTheTenantAdminCapability(t *testing.T) {
	if _, ok := pathguard.New(standardRoster()).(speedhttp.TenantAdminRouteGuard); ok {
		t.Fatal("the shared guard claims the tenant administration capability, " +
			"which would let a caller apply a guard without the platform-domain refusal")
	}
}

// TestTheSharedGuardAdmitsTheSystemTenant holds the rule the tenant management
// surfaces add and the shared guard does not: the members, invitations and SSO
// groups manage the system tenant itself, so a request resolved to the platform
// domain and addressed by it is legitimate there.
func TestTheSharedGuardAdmitsTheSystemTenant(t *testing.T) {
	// The route is addressed by the name; the identifier is not a path name.
	for path, want := range map[string]int{
		"/tenants/system/members":   nethttp.StatusNotFound,
		"/tenants/platform/members": nethttp.StatusOK,
	} {
		t.Run(path, func(t *testing.T) {
			got := send(sharedEngine("system", standardRoster()), path)
			if got.Code != want {
				t.Fatalf("%s answered %d, want %d (body %q)", path, got.Code, want, got.Body.String())
			}
		})
	}
}

// TestTheTenantAdminGuardRefusesTheSystemTenant holds the tenant management
// surfaces' additional rule: administration acts on a tenant, and the platform
// domain is not one, so the same request the shared guard admits is refused
// with the same bare 404, by identifier or by name.
func TestTheTenantAdminGuardRefusesTheSystemTenant(t *testing.T) {
	for _, path := range []string{"/tenants/system/members", "/tenants/platform/members"} {
		t.Run(path, func(t *testing.T) {
			got := send(tenantAdminEngine("system", standardRoster()), path)
			if got.Code != nethttp.StatusNotFound {
				t.Fatalf("%s answered %d, want %d (body %q)", path, got.Code, nethttp.StatusNotFound, got.Body.String())
			}
			requireBare404(t, got)
		})
	}
}

// TestTheTenantAdminGuardKeepsTheIdentityRule holds that the stricter guard
// still answers the identity mismatch, and admits a tenant it does administer
// by identifier or name.
func TestTheTenantAdminGuardKeepsTheIdentityRule(t *testing.T) {
	cases := []struct {
		name     string
		resolved tenant.ID
		path     string
		status   int
	}{
		{"a managed tenant by identifier", "t_acme", "/tenants/t_acme/members", nethttp.StatusNotFound},
		{"a managed tenant by name", "t_acme", "/tenants/acme/members", nethttp.StatusOK},
		{"another tenant named in the path", "t_acme", "/tenants/beta/members", nethttp.StatusNotFound},
		{"the platform domain named against another tenant", "t_acme", "/tenants/system/members", nethttp.StatusNotFound},
		{"the path names no tenant", "t_acme", "/tenants/ghost/members", nethttp.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := send(tenantAdminEngine(tc.resolved, standardRoster()), tc.path)
			if got.Code != tc.status {
				t.Fatalf("%s answered %d, want %d (body %q)", tc.path, got.Code, tc.status, got.Body.String())
			}
			if tc.status == nethttp.StatusNotFound {
				requireBare404(t, got)
			}
		})
	}
}

// TestAnUnresolvedRequestIsRefusedOnATenantAddressedRoute holds the fail-closed
// side: a tenant-addressed path with no tenant in the request cannot be
// reconciled, so it is refused like any other mismatch rather than passed on.
// Both guards answer it the same way.
func TestAnUnresolvedRequestIsRefusedOnATenantAddressedRoute(t *testing.T) {
	for name, e := range map[string]*gin.Engine{
		"the shared guard":       sharedEngine("", standardRoster()),
		"the tenant admin guard": tenantAdminEngine("", standardRoster()),
	} {
		t.Run(name, func(t *testing.T) {
			got := send(e, "/tenants/acme/members")
			if got.Code != nethttp.StatusNotFound {
				t.Fatalf("the request answered %d, want %d", got.Code, nethttp.StatusNotFound)
			}
			requireBare404(t, got)
		})
	}
}

// TestTheSharedGuardHandsOnTheInternalIdentifier holds the other half of the
// name boundary: a handler behind the guard binds the tenant's identifier, not
// the name the request carried.
func TestTheSharedGuardHandsOnTheInternalIdentifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(tenantctx.WithID(c.Request.Context(), "t_acme"))
		c.Next()
	})
	var bound string
	e.GET("/tenants/:tenant/members", pathguard.New(standardRoster()).Guard(func(c *gin.Context) {
		bound = c.Param("tenant")
		c.Status(nethttp.StatusOK)
	}))
	got := send(e, "/tenants/acme/members")
	if got.Code != nethttp.StatusOK {
		t.Fatalf("answered %d, want 200", got.Code)
	}
	if bound != "t_acme" {
		t.Fatalf("the handler bound %q, want the internal identifier t_acme", bound)
	}
}

// requireBare404 holds the answer to the shape the resolving layer writes for a
// tenant it does not serve: the status alone and the default problem document,
// with no type of the layer's own.
func requireBare404(t *testing.T, got *httptest.ResponseRecorder) {
	t.Helper()
	if ct := got.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("the response's media type is %q, want application/problem+json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatalf("the response body %q is not a JSON object: %v", got.Body.String(), err)
	}
	if body["type"] != "about:blank" {
		t.Fatalf("the response type is %v, want about:blank", body["type"])
	}
	if body["status"] != float64(nethttp.StatusNotFound) {
		t.Fatalf("the response status is %v, want %d", body["status"], nethttp.StatusNotFound)
	}
}
