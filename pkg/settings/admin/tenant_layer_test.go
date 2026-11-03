package admin

import (
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
)

// policyDecision is one (permission, domain) pair a policy guard decides on.
type policyDecision struct {
	perm   authz.Permission
	domain authz.Domain
}

// policyGuard answers from a closed set of (permission, domain) pairs. It is how
// a case holds that a layer is decided on one set of codes and not another: a
// permission the policy grants in another domain is not granted here, and a
// grant of the wrong code in the right domain is not a grant either. The real
// guard walks the policy engine; this one lets a case state its answer without
// one.
type policyGuard struct {
	mu      sync.Mutex
	allowed map[policyDecision]bool
	asked   []policyDecision
}

func newPolicyGuard(allowed ...policyDecision) *policyGuard {
	g := &policyGuard{allowed: make(map[policyDecision]bool, len(allowed))}
	for _, d := range allowed {
		g.allowed[d] = true
	}
	return g
}

func (g *policyGuard) note(p authz.Permission, domain authz.Domain) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := policyDecision{perm: p, domain: domain}
	g.asked = append(g.asked, d)
	return g.allowed[d]
}

func (g *policyGuard) Allowed(_ *gin.Context, perm authz.Permission) (bool, error) {
	return g.note(perm, ""), nil
}

func (g *policyGuard) RequirePermission(c *gin.Context, perm authz.Permission) bool {
	if g.note(perm, "") {
		return true
	}
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
	return false
}

func (g *policyGuard) RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	if g.note(perm, domain) {
		return true
	}
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
	return false
}

// Tenant-admin in acme allows the tenant layer's read and write codes in that
// tenant. A platform staff member holding that binding can still read and write
// the tenant layer through the platform surface: the surface asks the tenant
// codes in the addressed tenant's domain, not the global ones.
func TestAPlatformStaffMemberWithATenantAdminBindingReachesThatTenantLayer(t *testing.T) {
	declared := []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	t.Run("read", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionTenantSettingsRead, authz.Domain("acme")},
			policyDecision{authz.PermissionTenantSettingsWrite, authz.Domain("acme")},
		)
		w := r.do(http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=acme", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the read ended with %d, want 200: %s", w.Code, w.Body.String())
		}
	})

	t.Run("write", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionTenantSettingsRead, authz.Domain("acme")},
			policyDecision{authz.PermissionTenantSettingsWrite, authz.Domain("acme")},
		)
		w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme",
			map[string]any{"value": "manual"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("the write ended with %d, want 204: %s", w.Code, w.Body.String())
		}
	})
}

// A platform administrator who holds the global codes in the platform domain and
// no tenant-admin binding in the addressed tenant is refused on that tenant's
// layer. Holding settings.read in the tenant's domain is not the tenant code and
// does not stand in for it; the two sets are decided apart.
func TestAPlatformAdministratorWithoutATenantBindingIsRefusedOnATenantLayer(t *testing.T) {
	declared := []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	for name, target := range map[string]struct {
		method string
		target string
		body   any
	}{
		"read":  {http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=acme", nil},
		"write": {http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme", map[string]any{"value": "manual"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = declared
			// The platform codes in the platform domain are what a platform
			// administrator holds. The global code granted in the tenant's domain
			// is the trap the switch closes: under the old behaviour the surface
			// asked this code there, and the write would land.
			r.p.guard = newPolicyGuard(
				policyDecision{authz.PermissionSettingsRead, authz.PlatformDomain},
				policyDecision{authz.PermissionSettingsWrite, authz.PlatformDomain},
				policyDecision{authz.PermissionSettingsRead, authz.Domain("acme")},
				policyDecision{authz.PermissionSettingsWrite, authz.Domain("acme")},
			)
			w := r.do(target.method, target.target, target.body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("the request ended with %d, want 403: %s", w.Code, w.Body.String())
			}
			if calls := r.store.recorded(); len(calls) != 0 {
				t.Errorf("the settings surface was reached (%v) without the tenant codes", calls)
			}
		})
	}
}

// The global layer keeps the global codes and the platform domain: the tenant
// codes do not stand in for them, and a caller holding them cannot reach the
// global layer.
func TestTheGlobalLayerKeepsTheGlobalCodesAndThePlatformDomain(t *testing.T) {
	declared := []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	t.Run("a platform grant reaches the global layer", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionSettingsRead, authz.PlatformDomain},
			policyDecision{authz.PermissionSettingsWrite, authz.PlatformDomain},
		)
		if w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil); w.Code != http.StatusOK {
			t.Fatalf("the read ended with %d, want 200: %s", w.Code, w.Body.String())
		}
		if w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=global",
			map[string]any{"value": "on"}); w.Code != http.StatusNoContent {
			t.Fatalf("the write ended with %d, want 204: %s", w.Code, w.Body.String())
		}
	})

	t.Run("a tenant grant does not reach the global layer", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionTenantSettingsRead, authz.Domain("acme")},
			policyDecision{authz.PermissionTenantSettingsWrite, authz.Domain("acme")},
		)
		if w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil); w.Code != http.StatusForbidden {
			t.Fatalf("the read ended with %d, want 403: %s", w.Code, w.Body.String())
		}
	})
}
