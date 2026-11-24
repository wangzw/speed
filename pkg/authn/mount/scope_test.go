package mount

import (
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func scopedOn(items string) string {
	if items == "" {
		return `{}`
	}
	return `{"authn":{"gate":{"mount":{"endpoints":{` + items + `}}}}}`
}

// TestEachScopeMountsOnItsOwnEndpoint holds the ordinary and platform routes on the account endpoint and tenant routes on the tenant endpoint.
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

// TestTheSystemScopeFallsBackToTheDefaultEndpoint holds that platform management shares the account endpoint unless separated.
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

// TestTenantScopedGroupsTakeTheSharedGuard holds the shared identity guard on every tenant registration.
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

// TestATenantScopeRegistrationNeedsThePathTenantGuard holds that a
// tenant-scoped group cannot be mounted on a bare chain.
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

// TestTheTenantScopesEndpointMustBeNamed holds that the tenant scope has no fallback.
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
