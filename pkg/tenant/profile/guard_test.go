package profile

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/pathguard"
)

func guardRig(t *testing.T, resolved string) *rig {
	t.Helper()
	r := newRig(t)
	r.catalog.rows = []tenant.Tenant{
		{ID: "acme", Name: "acme-name", Status: tenant.StatusActive, Profile: sampleTenant().Profile},
		{ID: "other", Name: "other", Status: tenant.StatusActive},
		{ID: "system", Name: "system", Status: tenant.StatusActive},
	}
	r.tenantID = resolved
	r.p.routeGuard = pathguard.NewTenantAdmin(r.catalog)
	r.mount()
	return r
}

// TestAPathNamingAnotherTenantIsNotFound checks that a caller cannot reach
// another tenant's profile by naming it in the path.
func TestAPathNamingAnotherTenantIsNotFound(t *testing.T) {
	r := guardRig(t, "acme")

	w := r.do(http.MethodGet, "/api/v1/tenants/other/profile", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %q", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked %v on a cross-tenant path, want nothing", asked)
	}
}

// TestAPathNamingThePlatformDomainIsNotFound checks that the platform domain is
// refused even for a caller resolved to it.
func TestAPathNamingThePlatformDomainIsNotFound(t *testing.T) {
	r := guardRig(t, "system")

	w := r.do(http.MethodGet, "/api/v1/tenants/system/profile", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %q", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked %v on the platform domain, want nothing", asked)
	}
}

// TestAnNamePathAddressesTheResolvedTenant checks that the policy decision uses
// the resolved tenant's identifier, not the path's name.
func TestAnNamePathAddressesTheResolvedTenant(t *testing.T) {
	r := guardRig(t, "acme")

	w := r.do(http.MethodGet, "/api/v1/tenants/acme-name/profile", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	if domains := r.guard.domainsAsked(); len(domains) != 1 || domains[0] != "acme" {
		t.Errorf("domains asked = %v, want [acme]: the decision belongs to the resolved identifier", domains)
	}
}

// TestAPathNamingNoTenantIsNotFound holds a segment the roster resolves to
// nothing.
func TestAPathNamingNoTenantIsNotFound(t *testing.T) {
	r := guardRig(t, "acme")

	w := r.do(http.MethodGet, "/api/v1/tenants/ghost/profile", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %q", w.Code, w.Body.String())
	}
}
