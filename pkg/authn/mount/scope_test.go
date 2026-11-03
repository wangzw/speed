package mount

import (
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// scopedOn is the configuration body that names one endpoint per scope. A scope
// left out of items keeps the key out.
func scopedOn(items string) string {
	if items == "" {
		return `{}`
	}
	return `{"authn":{"gate":{"mount":{"endpoints":{` + items + `}}}}}`
}

// TestEachScopeMountsOnItsOwnEndpoint holds the point of the per-scope endpoint:
// the ordinary routes and the platform-management routes stay on the account
// endpoint, while the tenant-management routes mount on the endpoint the tenant
// layers guard. The base path each scope is published under is unchanged, so a
// route's address says which scope it belongs to and the endpoint says which
// chain decides it.
func TestEachScopeMountsOnItsOwnEndpoint(t *testing.T) {
	l := &ledger{}
	err := stack{
		body: scopedOn(`"default":"account","tenants":"api","system":"account"`),
		router: &standInRouter{
			names: []string{"account", "api"}, ledger: l,
		},
		gate: standInGate{ledger: l}, realGateModule: true,
		routeGuard: passthroughGuard{},
		register: func(m mount.Mounter) {
			m.Public("GET", "/ping", handler)
			m.Guarded("GET", "/auth/accounts", handler, mount.InScope(speedhttp.ScopeSystem))
			m.Guarded("GET", "/members", handler, mount.InScope(speedhttp.ScopeTenants))
		},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	routes, access := l.snapshot()
	wantRoutes := []routeCall{
		{"account", "GET", "/api/v1/auth/ping"},
		{"account", "GET", "/api/v1/system/auth/accounts"},
		{"api", "GET", "/api/v1/tenants/members"},
	}
	if !slices.Equal(routes, wantRoutes) {
		t.Errorf("the routes went to\n got %v\nwant %v", routes, wantRoutes)
	}
	wantAccess := []accessCall{
		{"Exempt", "account", "GET", "/api/v1/auth/ping"},
	}
	if !slices.Equal(access, wantAccess) {
		t.Errorf("the access levels went to\n got %v\nwant %v", access, wantAccess)
	}
}

// TestTheSystemScopeFallsBackToTheDefaultEndpoint holds that only the tenant
// scope needs an endpoint of its own: platform management and the ordinary
// surface share the account endpoint unless the configuration separates them,
// so an assembly that names none but the default keeps one place for both.
func TestTheSystemScopeFallsBackToTheDefaultEndpoint(t *testing.T) {
	l := &ledger{}
	err := stack{
		body:   scopedOn(`"default":"account"`),
		router: &standInRouter{names: []string{"account", "api"}, ledger: l},
		gate:   standInGate{ledger: l}, realGateModule: true,
		register: func(m mount.Mounter) {
			m.Guarded("GET", "/auth/accounts", handler, mount.InScope(speedhttp.ScopeSystem))
		},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	routes, _ := l.snapshot()
	want := []routeCall{{"account", "GET", "/api/v1/system/auth/accounts"}}
	if !slices.Equal(routes, want) {
		t.Errorf("the system route went to %v, want %v", routes, want)
	}
}

// TestTenantScopedGroupsTakeTheSharedGuard holds that the mounter applies the
// shared identity guard to every ScopeTenants registration and never the
// tenant administration refusal. The members, invitations and SSO groups manage
// the system tenant itself and are addressed by the platform domain
// legitimately, so narrowing them here would break that surface; only the two
// tenant management modules narrow the rule themselves.
func TestTenantScopedGroupsTakeTheSharedGuard(t *testing.T) {
	guard := &scopedGuard{}
	l := &ledger{}
	err := stack{
		body:   scopedOn(`"default":"account","tenants":"api"`),
		router: &standInRouter{names: []string{"account", "api"}, ledger: l},
		gate:   standInGate{ledger: l}, realGateModule: true,
		routeGuard: guard,
		register: func(m mount.Mounter) {
			m.Guarded("GET", "/members", handler, mount.InScope(speedhttp.ScopeTenants))
			m.Guarded("GET", "/invitations", handler, mount.InScope(speedhttp.ScopeTenants))
			m.Guarded("GET", "/sso", handler, mount.InScope(speedhttp.ScopeTenants))
			m.Guarded("GET", "/auth/accounts", handler, mount.InScope(speedhttp.ScopeSystem))
		},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	shared, admin := guard.counts()
	if shared != 3 {
		t.Errorf("the shared guard wrapped %d handlers, want 3 (one per tenant-scoped group)", shared)
	}
	if admin != 0 {
		t.Errorf("the tenant administration guard wrapped %d handlers, want the mounter to apply the shared guard alone", admin)
	}
}

// TestATenantScopeRegistrationNeedsThePathTenantGuard holds that a tenant-scoped
// group cannot be mounted on a bare chain. The guard is what keeps a path
// segment from addressing another tenant, so an assembly that delivers none is
// refused at the registration instead of serving the group unguarded.
func TestATenantScopeRegistrationNeedsThePathTenantGuard(t *testing.T) {
	text, panicked := panicText(func() {
		_ = stack{
			body:   scopedOn(`"default":"account","tenants":"api"`),
			router: &standInRouter{names: []string{"account", "api"}, ledger: &ledger{}},
			gate:   standInGate{ledger: &ledger{}}, realGateModule: true,
			register: func(m mount.Mounter) {
				m.Guarded("GET", "/members", handler, mount.InScope(speedhttp.ScopeTenants))
			},
		}.drive(t, nothing)
	})
	if !panicked {
		t.Fatal("a tenant-scoped route mounted with no path-tenant guard did not panic")
	}
	for _, want := range []string{"path-tenant guard", "tenants", "/members"} {
		if !strings.Contains(text, want) {
			t.Errorf("the panic %q does not name %q", text, want)
		}
	}
}

// TestTheTenantScopesEndpointMustBeNamed holds that there is no fallback for the
// tenant scope: its routes make their decisions in a tenant policy domain, and
// mounting them on the ordinary endpoint would put them on a chain no tenant
// layer guards. The registration is the place the omission is visible, so it
// panics naming the key instead of mounting the route somewhere it does not
// belong.
func TestTheTenantScopesEndpointMustBeNamed(t *testing.T) {
	text, panicked := panicText(func() {
		_ = stack{
			body:   `{}`,
			router: &standInRouter{names: []string{"main"}, ledger: &ledger{}},
			gate:   standInGate{ledger: &ledger{}}, realGateModule: true,
			routeGuard: passthroughGuard{},
			register: func(m mount.Mounter) {
				m.Guarded("GET", "/members", handler, mount.InScope(speedhttp.ScopeTenants))
			},
		}.drive(t, nothing)
	})
	if !panicked {
		t.Fatal("a tenant-scoped route mounted with no tenant endpoint configured did not panic")
	}
	for _, want := range []string{"authn.gate.mount.endpoints.tenants", "tenants", "/members"} {
		if !strings.Contains(text, want) {
			t.Errorf("the panic %q does not name %q", text, want)
		}
	}
}
